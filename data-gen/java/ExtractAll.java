///usr/bin/env java --source 21 "$0" "$@"; exit $?
// ^^^ allows running as: java ExtractAll.java (JEP 458, JDK 21+)

import java.io.*;
import java.nio.file.*;
import java.util.*;
import java.util.zip.*;

/**
 * ExtractAll — Extracts MC data from a pre-downloaded server jar.
 *
 * Runs inside a Docker/Podman container with these mounted volumes:
 *   /cache     — pre-downloaded server jar (by Go host)
 *   /jsons     — output directory for JSON files
 *   /java      — this file + custom Java extractors (read-only)
 *
 * Arguments:
 *   First argument is the Minecraft version (e.g., "1.21.11").
 *
 * The server jar and language files are downloaded by the Go host before
 * this container runs. This file handles extraction and data generation:
 *   1. Extract inner jar from bundler format
 *   2. Run MC --all data generator → reports/*.json
 *   3. Copy reports to /jsons/<version>/
 *   4. Compile and run custom Java extractors
 */
public class ExtractAll {

    static String version;
    static Path cacheDir  = Path.of("/cache");
    static Path jsonsDir  = Path.of("/jsons");
    static Path javaDir   = Path.of("/java");

    public static void main(String[] args) throws Exception {
        if (args.length > 0) {
            version = args[0];
        } else {
            fatal("Usage: ExtractAll <version> (e.g., 1.21.11)");
        }

        // Allow running outside container with custom paths.
        String cacheEnv = System.getenv("MC_CACHE_DIR");
        String jsonsEnv = System.getenv("MC_JSONS_DIR");
        String javaEnv = System.getenv("MC_JAVA_DIR");
        if (cacheEnv != null) cacheDir = Path.of(cacheEnv);
        if (jsonsEnv != null) jsonsDir = Path.of(jsonsEnv);
        if (javaEnv != null) javaDir = Path.of(javaEnv);

        log("ExtractAll: version=%s", version);
        log("  cache:      %s", cacheDir);
        log("  jsons:      %s", jsonsDir);
        log("  java:       %s", javaDir);

        // Step 1: Find pre-downloaded server jar.
        Path serverJar = cacheDir.resolve(version + "-server.jar");
        if (!Files.exists(serverJar) || Files.size(serverJar) == 0) {
            fatal("Server jar not found at %s (should be downloaded by Go host)", serverJar);
        }
        log("");
        log("Server jar: %s (%s)", serverJar, humanSize(Files.size(serverJar)));

        // Step 2: Extract inner jar from bundler format.
        Path innerJar = extractInnerJar(serverJar);

        // Step 3: Run --all data generator.
        Path reportsDir = runDataGenerator(serverJar);

        // Step 4: Copy --all reports to output.
        Path outputDir = jsonsDir.resolve(version);
        Files.createDirectories(outputDir);
        copyReports(reportsDir, outputDir);
        copyVersionJson(innerJar, outputDir);

        // Step 5: Run custom extractors (if present).
        runCustomExtractors(serverJar, innerJar, outputDir);

        // Done.
        log("");
        log("Extraction complete. Output:");
        try (var stream = Files.list(outputDir)) {
            stream.sorted().forEach(p -> {
                try {
                    long size = Files.size(p);
                    log("  %-30s %s", p.getFileName(), humanSize(size));
                } catch (IOException e) {
                    log("  %s (error: %s)", p.getFileName(), e.getMessage());
                }
            });
        }
    }

    // --- Step 2: Extract inner jar ---

    static Path extractInnerJar(Path bundledJar) throws Exception {
        Path innerPath = cacheDir.resolve(version + "-inner.jar");
        if (Files.exists(innerPath) && Files.size(innerPath) > 0) {
            log("  Inner jar cached: %s (%s)", innerPath, humanSize(Files.size(innerPath)));
            return innerPath;
        }

        log("");
        log("Extracting inner server jar from bundler format...");

        try (ZipFile zip = new ZipFile(bundledJar.toFile())) {
            // Look for META-INF/versions/*/server-*.jar.
            ZipEntry serverEntry = null;
            for (var entries = zip.entries(); entries.hasMoreElements(); ) {
                ZipEntry e = entries.nextElement();
                if (e.getName().startsWith("META-INF/versions/") && e.getName().endsWith(".jar")) {
                    serverEntry = e;
                    break;
                }
            }

            if (serverEntry == null) {
                // Not bundled — just copy.
                log("  Not bundled, using jar directly.");
                Files.copy(bundledJar, innerPath, StandardCopyOption.REPLACE_EXISTING);
                return innerPath;
            }

            log("  Found: %s (%s)", serverEntry.getName(), humanSize(serverEntry.getSize()));
            try (InputStream in = zip.getInputStream(serverEntry)) {
                Files.copy(in, innerPath, StandardCopyOption.REPLACE_EXISTING);
            }
        }

        log("  Extracted: %s", humanSize(Files.size(innerPath)));
        return innerPath;
    }

    // --- Step 3: Run --all data generator ---

    static Path runDataGenerator(Path serverJar) throws Exception {
        // Create a temp working dir for the generator (it creates files in cwd).
        Path workDir = cacheDir.resolve(version + "-datagen");
        Files.createDirectories(workDir);

        Path reportsDir = workDir.resolve("generated").resolve("reports");
        if (Files.exists(reportsDir.resolve("registries.json"))) {
            log("");
            log("Data generator output cached, skipping --all.");
            return reportsDir;
        }

        log("");
        log("Running MC --all data generator...");

        ProcessBuilder pb = new ProcessBuilder(
            "java",
            "-DbundlerMainClass=net.minecraft.data.Main",
            "-jar", serverJar.toAbsolutePath().toString(),
            "--all"
        );
        pb.directory(workDir.toFile());
        pb.inheritIO();

        Process proc = pb.start();
        int exit = proc.waitFor();
        if (exit != 0) {
            fatal("Data generator failed with exit code %d", exit);
        }

        if (!Files.exists(reportsDir.resolve("registries.json"))) {
            fatal("Data generator did not produce registries.json");
        }

        log("  Data generator complete.");
        return reportsDir;
    }

    // --- Step 4: Copy reports ---

    static void copyReports(Path reportsDir, Path outputDir) throws Exception {
        log("");
        log("Copying reports to %s ...", outputDir);

        String[] files = {"registries.json", "blocks.json", "items.json", "packets.json",
                          "commands.json", "datapack.json"};

        for (String name : files) {
            Path src = reportsDir.resolve(name);
            if (Files.exists(src)) {
                Files.copy(src, outputDir.resolve(name), StandardCopyOption.REPLACE_EXISTING);
                log("  %-25s %s", name, humanSize(Files.size(src)));
            }
        }

        // items.json is only produced by the --all generator up to 1.21.11;
        // for 26.x it is written by the GenItems custom extractor instead.

        // Also copy json-rpc-api-schema.json if present (1.21.11+).
        Path schema = reportsDir.resolve("json-rpc-api-schema.json");
        if (Files.exists(schema)) {
            Files.copy(schema, outputDir.resolve("json-rpc-api-schema.json"),
                       StandardCopyOption.REPLACE_EXISTING);
        }
    }

    // --- Step 5: Custom extractors ---

    /**
     * version.json (id, protocol_version, world_version, pack versions, java_version) and the
     * English language file ship in the inner jar: en_us.json is the one language that is not
     * in Mojang's asset index (the launcher downloads the others), so it is copied from here.
     */
    static void copyVersionJson(Path innerJar, Path outputDir) throws Exception {
        try (var jar = new java.util.jar.JarFile(innerJar.toFile())) {
            copyJarEntry(jar, "version.json", outputDir.resolve("version.json"));
            Files.createDirectories(outputDir.resolve("lang"));
            copyJarEntry(jar, "assets/minecraft/lang/en_us.json", outputDir.resolve("lang").resolve("en_us.json"));
        }
    }

    static void copyJarEntry(java.util.jar.JarFile jar, String name, Path dest) throws Exception {
        var entry = jar.getJarEntry(name);
        if (entry == null) fatal("%s not found in %s", name, jar.getName());
        try (var in = jar.getInputStream(entry)) {
            Files.copy(in, dest, StandardCopyOption.REPLACE_EXISTING);
        }
        log("  %-25s %s", name, humanSize(entry.getSize()));
    }

    static void runCustomExtractors(Path serverJar, Path innerJar, Path outputDir) throws Exception {
        String[] extractors = {"GenEntities", "GenComponents", "GenBlockEntities", "GenBlockProperties", "GenBiomes", "GenComponentSchema", "GenItems", "GenPacketSchema"};
        List<String> found = new ArrayList<>();

        for (String name : extractors) {
            Path src = javaDir.resolve(name + ".java");
            if (Files.exists(src)) {
                found.add(name);
            }
        }

        if (found.isEmpty()) {
            log("");
            log("No custom extractors found in %s (optional, skipping).", javaDir);
            return;
        }

        // Extract library jars from the bundled server jar.
        String classpath = buildExtractorClasspath(serverJar, innerJar);

        log("");
        log("Compiling custom extractors: %s", String.join(", ", found));

        // Compile all found extractors into the cache, not next to the data.
        Path classesDir = cacheDir.resolve(version + "-classes");
        Files.createDirectories(classesDir);
        List<String> javacArgs = new ArrayList<>(List.of(
            "javac", "-cp", classpath, "-proc:none",
            "-d", classesDir.toAbsolutePath().toString()
        ));
        for (String name : found) {
            javacArgs.add(javaDir.resolve(name + ".java").toAbsolutePath().toString());
        }

        int compileExit = new ProcessBuilder(javacArgs)
            .inheritIO().start().waitFor();
        if (compileExit != 0) {
            fatal("extractor compilation failed (exit %d); the MC API moved — fix data-gen/java/*.java", compileExit);
        }

        // Run each extractor; they write their JSON into the working directory.
        String runCp = classpath + ":" + classesDir.toAbsolutePath();
        for (String name : found) {
            log("  Running %s...", name);
            ProcessBuilder pb = new ProcessBuilder(
                "java", "-cp", runCp, name
            );
            pb.directory(outputDir.toFile());
            pb.inheritIO();

            int exit = pb.start().waitFor();
            if (exit != 0) {
                fatal("%s failed with exit code %d", name, exit);
            }
        }

        // The extractors bootstrap the game, whose logger writes logs/ into the working directory.
        deleteTree(outputDir.resolve("logs"));
    }

    static void deleteTree(Path dir) throws IOException {
        if (!Files.exists(dir)) return;
        try (var stream = Files.walk(dir)) {
            stream.sorted(java.util.Comparator.reverseOrder()).forEach(p -> {
                try { Files.delete(p); } catch (IOException e) { throw new java.io.UncheckedIOException(e); }
            });
        }
    }

    /**
     * Extract library jars from the bundled server jar and build a classpath string.
     * The bundled jar contains META-INF/libraries/*.jar and META-INF/classpath-joined.
     */
    static String buildExtractorClasspath(Path serverJar, Path innerJar) throws Exception {
        Path libsDir = cacheDir.resolve(version + "-libs");
        Path doneMarker = libsDir.resolve(".extracted");

        if (!Files.exists(doneMarker)) {
            log("Extracting library jars from bundled server...");
            Files.createDirectories(libsDir);

            try (ZipFile zip = new ZipFile(serverJar.toFile())) {
                for (var entries = zip.entries(); entries.hasMoreElements(); ) {
                    ZipEntry e = entries.nextElement();
                    if (e.getName().startsWith("META-INF/libraries/") && e.getName().endsWith(".jar")) {
                        Path dest = libsDir.resolve(e.getName());
                        Files.createDirectories(dest.getParent());
                        try (InputStream in = zip.getInputStream(e)) {
                            Files.copy(in, dest, StandardCopyOption.REPLACE_EXISTING);
                        }
                    }
                }
            }
            Files.writeString(doneMarker, "ok");
            log("  Library jars extracted.");
        }

        // Build classpath: inner jar + all library jars.
        StringBuilder cp = new StringBuilder();
        cp.append(innerJar.toAbsolutePath());

        try (var stream = Files.walk(libsDir)) {
            stream.filter(p -> p.toString().endsWith(".jar")).forEach(p -> {
                cp.append(':').append(p.toAbsolutePath());
            });
        }

        return cp.toString();
    }

    // --- Utility ---

    static String humanSize(long bytes) {
        if (bytes >= 1024 * 1024) return String.format("%.1f MB", bytes / (1024.0 * 1024.0));
        if (bytes >= 1024) return String.format("%.1f KB", bytes / 1024.0);
        return bytes + " B";
    }

    static void log(String fmt, Object... args) {
        System.out.printf(fmt + "%n", args);
    }

    static void fatal(String fmt, Object... args) {
        System.err.printf("ExtractAll: " + fmt + "%n", args);
        System.exit(1);
    }
}
