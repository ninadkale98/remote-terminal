module github.com/rterm/rterm

go 1.24

require (
	github.com/creack/pty v1.1.24
	golang.org/x/term v0.0.0
)

require golang.org/x/sys v0.33.0

replace golang.org/x/sys => ./third_party/sys

replace golang.org/x/term => ./third_party/term
