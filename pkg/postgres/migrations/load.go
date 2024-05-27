package migrations

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

func LoadMigrations(filepath string) ([]Migration, error) {
	files, err := os.ReadDir(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %+v", err)
	}

	migrations := make([]Migration, 0, len(files))
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		query, err := os.ReadFile(filepath + "/" + file.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %+v", file.Name(), err)
		}

		index, err := getIndexFromFilenamePrefix(file.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to get index from filename %s: %+v", file.Name(), err)
		}

		migrations = append(migrations, Migration{
			Index: index,
			Query: string(query),
		})
	}

	return migrations, nil
}

var FILE_ID_PREFIX = regexp.MustCompile(`^(\d+)-`)

func getIndexFromFilenamePrefix(filename string) (int, error) {
	matches := FILE_ID_PREFIX.FindStringSubmatch(filename)
	if len(matches) != 2 {
		return 0, fmt.Errorf("failed to parse migration filename %s", filename)
	}

	return strconv.Atoi(matches[1])
}
