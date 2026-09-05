package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// PacketsJSON mirrors the structure of packets.json from MC --all.
// Top-level keys: phase names (login, status, configuration, play, handshake).
// Each phase has "clientbound" and/or "serverbound".
// Each direction maps packet name → {"protocol_id": N}.
type PacketsJSON map[string]map[string]map[string]struct {
	ProtocolID int `json:"protocol_id"`
}

type packetIDEntry struct {
	Name       string // e.g. "minecraft:add_entity"
	ProtocolID int
	GoName     string // e.g. "ClientboundAddEntity"
}

// packetSection is a const block for one phase+direction pair.
type packetSection struct {
	Comment string   // e.g. "Play Clientbound"
	Entries []string // pre-formatted lines for each const entry
}

// packetNameTable is the String() table of one id type.
type packetNameTable struct {
	Name    string   // ClientboundPacketID
	VarName string   // playClientboundNames
	Names   []string // constant name per id of the play state
}

// phases is loaded from hand-crafted/packet_phases.json.
// Defines protocol phases in generation order with Go naming conventions.
var phases []packetPhase

func genPacketID(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "packets.json")
	outPath := filepath.Join(goMCRoot, "data", "packetid", "packetid.go")

	if phases == nil {
		if err := readHandCrafted(goMCRoot, "packet_phases.json", &phases); err != nil {
			return fmt.Errorf("genPacketID: %w", err)
		}
	}

	var packets PacketsJSON
	if err := readJSON(jsonPath, &packets); err != nil {
		return fmt.Errorf("genPacketID: %w", err)
	}

	tmpl, err := loadTemplate(goMCRoot, "packetid.go.tmpl")
	if err != nil {
		return fmt.Errorf("genPacketID: %w", err)
	}

	sections := buildPacketSections(packets)

	// String() tables: the play state's names per id type.
	var types []packetNameTable
	for _, dir := range []string{"clientbound", "serverbound"} {
		var names []string
		for _, p := range sortPacketIDEntries(packets["play"][dir], dir, "") {
			for len(names) < p.ProtocolID {
				names = append(names, "")
			}
			names = append(names, p.GoName)
		}
		types = append(types, packetNameTable{
			Name:    dirTypeName(dir),
			VarName: "play" + dirPrefix(dir) + "Names",
			Names:   names,
		})
	}

	data := struct {
		Header   string
		Sections []packetSection
		Types    []packetNameTable
	}{
		Header:   generatedHeader("gen_packetid.go", "packets.json"),
		Sections: sections,
		Types:    types,
	}

	out, err := executeTemplate(tmpl, data)
	if err != nil {
		return fmt.Errorf("genPacketID: formatting: %w", err)
	}

	if err := writeFile(outPath, out); err != nil {
		return fmt.Errorf("genPacketID: %w", err)
	}
	logf("gen-packetid: wrote %s (%d bytes)", outPath, len(out))
	return nil
}

func buildPacketSections(packets PacketsJSON) []packetSection {
	var sections []packetSection

	for _, ph := range phases {
		phaseData, ok := packets[ph.Name]
		if !ok {
			continue
		}

		for _, dir := range []string{"clientbound", "serverbound"} {
			dirData, ok := phaseData[dir]
			if !ok {
				continue
			}

			pkts := sortPacketIDEntries(dirData, dir, ph.GoPrefix)

			// Sequential from 0 ⇒ use iota; otherwise explicit values.
			sequential := true
			for i, p := range pkts {
				if p.ProtocolID != i {
					sequential = false
					break
				}
			}

			typeName := dirTypeName(dir)
			var lines []string
			for i, p := range pkts {
				if sequential {
					if i == 0 {
						lines = append(lines, fmt.Sprintf("%s %s = iota", p.GoName, typeName))
					} else {
						lines = append(lines, p.GoName)
					}
				} else {
					lines = append(lines, fmt.Sprintf("%s %s = %d", p.GoName, typeName, p.ProtocolID))
				}
			}

			// Guard sentinel for play phase.
			if ph.Name == "play" {
				lines = append(lines, fmt.Sprintf("%sPacketIDGuard", dirPrefix(dir)))
			}

			dirTitle := strings.ToUpper(dir[:1]) + dir[1:]
			sections = append(sections, packetSection{
				Comment: fmt.Sprintf("%s %s", ph.Comment, dirTitle),
				Entries: lines,
			})
		}
	}

	return sections
}

func sortPacketIDEntries(dirData map[string]struct {
	ProtocolID int `json:"protocol_id"`
}, dir, abbrev string) []packetIDEntry {
	var pkts []packetIDEntry
	prefix := dirPrefix(dir)

	for name, info := range dirData {
		goName := packetGoName(name, prefix, abbrev)
		pkts = append(pkts, packetIDEntry{
			Name:       name,
			ProtocolID: info.ProtocolID,
			GoName:     goName,
		})
	}

	sort.Slice(pkts, func(i, j int) bool {
		return pkts[i].ProtocolID < pkts[j].ProtocolID
	})

	return pkts
}

func packetGoName(mcName, dirPrefix, phaseAbbrev string) string {
	// Strip "minecraft:" prefix.
	name := strings.TrimPrefix(mcName, "minecraft:")

	// Replace "/" and "_" with space, then CamelCase.
	name = strings.ReplaceAll(name, "/", " ")
	name = strings.ReplaceAll(name, "_", " ")

	var sb strings.Builder
	sb.WriteString(dirPrefix)
	sb.WriteString(phaseAbbrev)

	for _, word := range strings.Fields(name) {
		if len(word) == 0 {
			continue
		}
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		sb.WriteString(string(runes))
	}

	return sb.String()
}

func dirPrefix(dir string) string {
	if dir == "clientbound" {
		return "Clientbound"
	}
	return "Serverbound"
}

func dirTypeName(dir string) string {
	if dir == "clientbound" {
		return "ClientboundPacketID"
	}
	return "ServerboundPacketID"
}
