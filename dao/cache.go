package dao

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

type Cache struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Method  string `json:"-"`
	Item    string `json:"-"`
}

type Group struct {
	Name   string  `json:"name"`
	Caches []Cache `json:"caches"`
}

func LoadCaches(configPath string) ([]Group, error) {
	var groups []Group

	_, err := os.Stat(configPath)
	if err != nil {
		return groups, err
	}

	fileContent, err := ioutil.ReadFile(configPath)
	if err != nil {
		return groups, err
	}

	err = json.Unmarshal(fileContent, &groups)

	return groups, err
}
