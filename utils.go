package main

import (
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

func ReadYamlFile(filePath string, out interface{}) (err error) {
	yamlFile, err := ioutil.ReadFile(filePath)
	if err != nil {
		return
	}
	if err = yaml.Unmarshal(yamlFile, out); err != nil {
		return
	}
	return
}
