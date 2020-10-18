package cbactions

import (
	"os"
	"strings"
)

func RewritePath(path string) string {
	if stringInSlice(path, []string{
		"/runner/.credentials",
		"/runner/.credentials_rsaparams",
		"/runner/.env",
		"/runner/.path",
		"/runner/.runner",
	}) {
		path = strings.ReplaceAll(path, "/runner", os.Getenv("CBA_PATH_SUBSTITUTION"))
	}

	return path
}

func stringInSlice(needle string, haystack []string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}

	return false
}
