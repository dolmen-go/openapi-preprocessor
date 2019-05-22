package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	yaml "gopkg.in/yaml.v3"
)

func loadURL(u *url.URL) (map[string]interface{}, error) {
	if u.Scheme != "file" {
		return nil, fmt.Errorf("unsupported %q URL scheme", u.Scheme)
	}

	return loadFile(filepath.FromSlash(u.Path))
}

func loadFile(pth string) (map[string]interface{}, error) {
	f, err := os.Open(pth)
	if err != nil {
		return nil, err
	}
	var load func(io.Reader) (map[string]interface{}, error)
	switch filepath.Ext(pth) {
	case ".json":
		load = loadJSON
	case ".yaml", ".yml":
		load = loadYAML
	default:
		return nil, errors.New("unsupported file extension")
	}
	return load(f)
}

func loadYAML(r io.Reader) (map[string]interface{}, error) {
	data, err := loadAny(yaml.NewDecoder(r))
	if err != nil {
		return nil, err
	}
	return fixMaps(data).(map[string]interface{}), err
}

func fixMaps(v interface{}) interface{} {
	switch v := v.(type) {
	case nil, bool, string, int, int64, float64:
	case []interface{}:
		for i, item := range v {
			v[i] = fixMaps(item)
		}
	case map[interface{}]interface{}:
		m := make(map[string]interface{}, len(v))
		for key, val := range v {
			m[fmt.Sprint(key)] = fixMaps(val)
		}
		return m
	case map[string]interface{}:
		for key, value := range v {
			v[key] = fixMaps(value)
		}
	}
	return v
}

func loadJSON(r io.Reader) (map[string]interface{}, error) {
	return loadAny(json.NewDecoder(r))
}

func loadAny(decoder interface{ Decode(interface{}) error }) (map[string]interface{}, error) {
	var data map[string]interface{}
	err := decoder.Decode(&data)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("unexpected empty object")
	}
	return data, err
}
