module github.com/mj41/mc26

go 1.25

require github.com/mj41/go-mc26 v0.0.0-00010101000000-000000000000

// The library sources live in gen/src (their own module, without the generated
// packages); the generators only need its nbt package.
replace github.com/mj41/go-mc26 => ./gen/src
