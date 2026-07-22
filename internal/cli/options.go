package cli

func canonicalOption(value string) string {
	switch value {
	case "-h":
		return "--help"
	case "-j":
		return "--json"
	case "-o":
		return "--output"
	case "-y":
		return "--yes"
	case "-n":
		return "--name"
	case "-i":
		return "--id"
	case "-4":
		return "--ipv4"
	case "-a":
		return "--all"
	case "-d":
		return "--detail"
	case "-e":
		return "--editor"
	case "-u":
		return "--full-id"
	case "-l":
		return "--lines"
	case "-f":
		return "--follow"
	case "-r":
		return "--raw"
	case "-s":
		return "--short"
	default:
		return value
	}
}

func countCanonicalOption(args []string, wanted string) int {
	count := 0
	for _, value := range args {
		if canonicalOption(value) == wanted {
			count++
		}
	}
	return count
}
