package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	dirMode  = 0o700
	fileMode = 0o600

	modelsPrefix     = "models."
	contextWindow    = ".context_window"
	providersPrefix  = "providers."
	baseURLSuffix    = ".base_url"
	providerReserved = "default" // the flat provider/base_url keys, as a name
)

// ModelOverride is a per-model setting.
type ModelOverride struct {
	ContextWindow int `json:"context_window"`
}

// ProviderEntry is one named provider: an endpoint that speaks Chat
// Completions. The name is what model refs address ("vllm/qwen3").
type ProviderEntry struct {
	BaseURL string `json:"base_url,omitempty"`
}

// File is one config layer (global, project, or local JSON).
type File struct {
	Values    map[string]string        // plain settings
	Models    map[string]ModelOverride // models.<name>.*
	Providers map[string]ProviderEntry // providers.<name>.base_url
}

// UnmarshalJSON accepts string or numeric values for plain keys.
func (f *File) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	f.Values, f.Models, f.Providers = map[string]string{}, nil, nil
	for k, v := range raw {
		switch k {
		case "models":
			if err := json.Unmarshal(v, &f.Models); err != nil {
				return fmt.Errorf("models: %w", err)
			}
			continue
		case "providers":
			if err := json.Unmarshal(v, &f.Providers); err != nil {
				return fmt.Errorf("providers: %w", err)
			}
			continue
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			s = string(bytes.TrimSpace(v))
		}
		f.Values[k] = s
	}
	return nil
}

// MarshalJSON writes plain keys as strings and models as an object.
func (f File) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	for k, v := range f.Values {
		m[k] = v
	}
	if len(f.Models) > 0 {
		m["models"] = f.Models
	}
	if len(f.Providers) > 0 {
		m["providers"] = f.Providers
	}
	return json.Marshal(m)
}

// LoadFile reads path; a missing file is an empty File.
func LoadFile(path string) (File, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Save writes the file atomically with mode 0600, creating the dir 0700.
func (f File) Save(path string) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// modelKey parses models.<name>.context_window.
func modelKey(key string) (name string, ok bool) {
	if !strings.HasPrefix(key, modelsPrefix) || !strings.HasSuffix(key, contextWindow) {
		return "", false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(key, modelsPrefix), contextWindow)
	return name, name != ""
}

// providerKey parses providers.<name>.base_url.
func providerKey(key string) (name string, ok bool) {
	if !strings.HasPrefix(key, providersPrefix) || !strings.HasSuffix(key, baseURLSuffix) {
		return "", false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(key, providersPrefix), baseURLSuffix)
	if err := validProviderName(name); err != nil {
		return "", false
	}
	return name, true
}

// validProviderName rejects names that cannot be addressed as <name>/model:
// empty, or containing the key or ref separators.
func validProviderName(name string) error {
	if name == "" {
		return errors.New("provider name must not be empty")
	}
	if strings.ContainsAny(name, "./") {
		return fmt.Errorf("provider name must not contain '.' or '/': %q", name)
	}
	return nil
}

// Set validates and stores key=value, returning the updated File.
func (f File) Set(key, value string) (File, error) {
	out := f.clone()
	if name, ok := modelKey(key); ok {
		if err := positiveInt(value); err != nil {
			return f, fmt.Errorf("%s: %w", key, err)
		}
		n, _ := strconv.Atoi(value)
		out.Models[name] = ModelOverride{ContextWindow: n}
		return out, nil
	}
	if name, ok := providerKey(key); ok {
		if err := validURL(value); err != nil {
			return f, fmt.Errorf("%s: %w", key, err)
		}
		if out.Providers == nil {
			out.Providers = map[string]ProviderEntry{}
		}
		out.Providers[name] = ProviderEntry{BaseURL: value}
		return out, nil
	}
	spec, ok := LookupKey(key)
	if !ok {
		return f, unknownKey(key)
	}
	if err := spec.Validate(value); err != nil {
		return f, fmt.Errorf("%s: %w", key, err)
	}
	out.Values[key] = value
	return out, nil
}

// Unset removes key, returning the updated File.
func (f File) Unset(key string) (File, error) {
	out := f.clone()
	if name, ok := modelKey(key); ok {
		delete(out.Models, name)
		return out, nil
	}
	if name, ok := providerKey(key); ok {
		delete(out.Providers, name)
		return out, nil
	}
	if _, ok := LookupKey(key); !ok {
		return f, unknownKey(key)
	}
	delete(out.Values, key)
	return out, nil
}

// Stored returns the value stored in the file for key.
func (f File) Stored(key string) (string, bool, error) {
	if name, ok := modelKey(key); ok {
		m, found := f.Models[name]
		return strconv.Itoa(m.ContextWindow), found, nil
	}
	if name, ok := providerKey(key); ok {
		p, found := f.Providers[name]
		return p.BaseURL, found, nil
	}
	if _, ok := LookupKey(key); !ok {
		return "", false, unknownKey(key)
	}
	v, found := f.Values[key]
	return v, found, nil
}

// ContextOverrides returns model -> context window for ContextWindow.
func (f File) ContextOverrides() map[string]int {
	out := map[string]int{}
	for n, m := range f.Models {
		out[n] = m.ContextWindow
	}
	return out
}

// ModelNames returns override names sorted.
func (f File) ModelNames() []string {
	var n []string
	for k := range f.Models {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func (f File) clone() File {
	out := File{Values: map[string]string{}, Models: map[string]ModelOverride{}, Providers: map[string]ProviderEntry{}}
	for k, v := range f.Values {
		out.Values[k] = v
	}
	for k, v := range f.Models {
		out.Models[k] = v
	}
	for k, v := range f.Providers {
		out.Providers[k] = v
	}
	return out
}

func unknownKey(key string) error {
	names := []string{}
	for _, k := range Keys {
		names = append(names, k.Name)
	}
	return fmt.Errorf("unknown setting %q: want one of %s, models.<name>.context_window or providers.<name>.base_url", key, strings.Join(names, ", "))
}
