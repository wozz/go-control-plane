package apiversions

//go:generate go run golang.org/x/tools/cmd/stringer -type=APIVersion

type APIVersion int

const (
	V2 APIVersion = iota
	V3
	UnknownVersion
)
