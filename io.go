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

func loadURL(u *url.URL) (map[string]any, error) {
	if u.Scheme != "file" {
		return nil, fmt.Errorf("unsupported %q URL scheme", u.Scheme)
	}

	return loadFile(urlPathToOSPath(u.Path))
}

func loadFile(pth string) (map[string]any, error) {
	f, err := os.Open(pth)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var load func(io.Reader) (map[string]any, error)
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

func loadYAML(r io.Reader) (map[string]any, error) {
	data, err := loadAny(yaml.NewDecoder(r))
	if err != nil {
		return nil, err
	}
	return fixMaps(data).(map[string]any), err
}

func fixMaps(v any) any {
	switch v := v.(type) {
	case nil, bool, string, int, int64, float64:
	case []any:
		for i, item := range v {
			v[i] = fixMaps(item)
		}
	case map[any]any:
		m := make(map[string]any, len(v))
		for key, val := range v {
			m[fmt.Sprint(key)] = fixMaps(val)
		}
		return m
	case map[string]any:
		for key, value := range v {
			v[key] = fixMaps(value)
		}
	}
	return v
}

func loadJSON(r io.Reader) (map[string]any, error) {
	dec := json.NewDecoder(r)
	data, err := loadAny(dec)
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after JSON content")
	}
	return data, err
}

func loadAny(decoder interface{ Decode(any) error }) (map[string]any, error) {
	var data map[string]any
	err := decoder.Decode(&data)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("unexpected empty object")
	}
	return data, err
}
