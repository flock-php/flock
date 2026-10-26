package certs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// FirefoxPolicyDir is where Firefox on Linux reads its enterprise policy, the Snap included (it
// may read /etc/firefox, and nothing else of /etc). Tests move it.
var FirefoxPolicyDir = "/etc/firefox/policies"

// firefoxCA is the copy of user's CA the policy installs: where Snap's Firefox can read it. Every
// user of the computer has their own Flock CA.
func firefoxCA(user string) string { return filepath.Join(FirefoxPolicyDir, "flock-"+user+".crt") }

func firefoxPolicyFile() string { return filepath.Join(FirefoxPolicyDir, "policies.json") }

// FirefoxPolicyInstalled reports whether Firefox's policy installs user's CA at caPath.
func FirefoxPolicyInstalled(user, caPath string) bool {
	want, err := os.ReadFile(caPath)
	if err != nil {
		return false
	}
	have, err := os.ReadFile(firefoxCA(user))
	if err != nil || !bytes.Equal(have, want) {
		return false
	}
	doc, err := readPolicies()
	return err == nil && slices.Contains(installList(doc), firefoxCA(user))
}

// InstallFirefoxPolicy makes Firefox add user's CA at caPath to every profile when it starts, the
// ones created later too. Policies an administrator set are kept. Needs root.
func InstallFirefoxPolicy(user, caPath string) error {
	data, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	doc, err := readPolicies()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(FirefoxPolicyDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(firefoxCA(user), data, 0o644); err != nil {
		return err
	}
	list := installList(doc)
	if slices.Contains(list, firefoxCA(user)) {
		return nil
	}
	setInstallList(doc, append(list, firefoxCA(user)))
	return writePolicies(doc)
}

// RemoveFirefoxPolicy takes user's CA out of Firefox's policy, and the policy file with it when
// nothing else is in it. Needs root.
func RemoveFirefoxPolicy(user string) error {
	doc, err := readPolicies()
	if err != nil {
		return err
	}
	errs := []error{}
	if list := installList(doc); slices.Contains(list, firefoxCA(user)) {
		setInstallList(doc, slices.DeleteFunc(list, func(p string) bool { return p == firefoxCA(user) }))
		if policies, _ := doc["policies"].(map[string]any); len(policies) == 0 {
			errs = append(errs, os.Remove(firefoxPolicyFile()))
		} else {
			errs = append(errs, writePolicies(doc))
		}
	}
	if err := os.Remove(firefoxCA(user)); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	_ = os.Remove(FirefoxPolicyDir) // only when empty
	_ = os.Remove(filepath.Dir(FirefoxPolicyDir))
	return errors.Join(errs...)
}

// readPolicies reads policies.json, an empty document when there is none.
func readPolicies() (map[string]any, error) {
	data, err := os.ReadFile(firefoxPolicyFile())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, errors.New(firefoxPolicyFile() + ": " + err.Error())
	}
	return doc, nil
}

func writePolicies(doc map[string]any) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(firefoxPolicyFile(), append(data, '\n'), 0o644)
}

// installList is policies.Certificates.Install.
func installList(doc map[string]any) []string {
	policies, _ := doc["policies"].(map[string]any)
	certificates, _ := policies["Certificates"].(map[string]any)
	raw, _ := certificates["Install"].([]any)
	var list []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			list = append(list, s)
		}
	}
	return list
}

// setInstallList sets policies.Certificates.Install, removing what becomes empty.
func setInstallList(doc map[string]any, list []string) {
	policies, _ := doc["policies"].(map[string]any)
	if policies == nil {
		policies = map[string]any{}
		doc["policies"] = policies
	}
	certificates, _ := policies["Certificates"].(map[string]any)
	if certificates == nil {
		certificates = map[string]any{}
		policies["Certificates"] = certificates
	}
	if len(list) > 0 {
		certificates["Install"] = list
		return
	}
	delete(certificates, "Install")
	if len(certificates) == 0 {
		delete(policies, "Certificates")
	}
}
