package provision

import (
	"bytes"
	"testing"
)

func TestComparePrefersExistingExecutable(t *testing.T) {
	cands := []Candidate{
		{Method: MethodPackageManager, Source: "npm", Ownership: OwnershipSystem, LifecycleScripts: true},
		{Method: MethodRelease, Asset: "tool-linux.tar.gz", Ownership: OwnershipGrokinstall},
		{Method: MethodExisting, Path: "/usr/local/bin/tool", Ownership: OwnershipNone},
	}
	ordered := Compare(cands)
	if ordered[0].Method != MethodExisting {
		t.Fatalf("expected the installed executable to win, got %s", ordered[0].Method)
	}
}

func TestCompareOrdersByPolicy(t *testing.T) {
	ordered := Compare([]Candidate{
		{Method: MethodSourceBuild},
		{Method: MethodRelease},
		{Method: MethodExisting},
		{Method: MethodSystemPackageManager},
	})
	var methods []Method
	for _, c := range ordered {
		methods = append(methods, c.Method)
	}
	want := []Method{MethodExisting, MethodRelease, MethodSourceBuild, MethodSystemPackageManager}
	for i := range want {
		if methods[i] != want[i] {
			t.Fatalf("order = %v, want %v", methods, want)
		}
	}
}

func TestCompareIsDeterministic(t *testing.T) {
	cands := []Candidate{
		{Method: MethodRelease, Asset: "b"},
		{Method: MethodRelease, Asset: "a"},
	}
	first := Compare(cands)
	second := Compare([]Candidate{cands[1], cands[0]})
	if first[0].Summary() != second[0].Summary() {
		t.Fatal("comparison must be deterministic regardless of input order")
	}
}

func TestSafeCandidateNeedsNoApproval(t *testing.T) {
	c := Candidate{Method: MethodRelease, Ownership: OwnershipGrokinstall}
	if !c.Safe() {
		t.Fatal("a release artifact in an owned runtime needs no approval")
	}
}

func TestPackageWithLifecycleScriptsIsNotSafe(t *testing.T) {
	c := Candidate{
		Method:           MethodPackageManager,
		Ownership:        OwnershipSystem,
		LifecycleScripts: true,
		Requires:         []Authorization{AuthInstallScripts},
	}
	if c.Safe() {
		t.Fatal("lifecycle scripts must block the safe policy")
	}
}

func TestSystemPackageManagerIsNeverSafe(t *testing.T) {
	c := Candidate{Method: MethodSystemPackageManager, Ownership: OwnershipSystem}
	if c.Safe() {
		t.Fatal("system package managers must never pass the safe policy")
	}
}

func TestPrivilegedIsNotSafe(t *testing.T) {
	if (Candidate{Method: MethodPackageManager, RequiresPrivilege: true}).Safe() {
		t.Fatal("privileged operations must not pass the safe policy")
	}
}

func TestSelectPicksSafestUnderSafePolicy(t *testing.T) {
	sel, refusal := Select([]Candidate{
		{Method: MethodPackageManager, Requires: []Authorization{AuthInstallScripts}, Ownership: OwnershipSystem},
		{Method: MethodRelease, Ownership: OwnershipGrokinstall},
	}, Policy{Mode: PolicySafe})
	if refusal != nil {
		t.Fatalf("expected a selection, got refusal: %+v", refusal)
	}
	if sel.Method != MethodRelease {
		t.Fatalf("selected %s, want release", sel.Method)
	}
}

func TestSelectRefusesWhenNothingIsSafe(t *testing.T) {
	sel, refusal := Select([]Candidate{
		{Method: MethodPackageManager, Requires: []Authorization{AuthInstallScripts}, Ownership: OwnershipSystem, LifecycleScripts: true},
	}, Policy{Mode: PolicySafe})
	if sel.Method != "" {
		t.Fatal("nothing should be selected")
	}
	if refusal == nil {
		t.Fatal("expected a structured refusal")
	}
	if len(refusal.Required) == 0 {
		t.Fatal("a refusal must name the specific authorization required")
	}
	if refusal.Required[0] != AuthInstallScripts {
		t.Fatalf("required = %v, want allow_install_scripts", refusal.Required)
	}
}

func TestSelectRespectsNever(t *testing.T) {
	sel, refusal := Select([]Candidate{{Method: MethodRelease, Ownership: OwnershipGrokinstall}}, Policy{Mode: PolicyNever})
	if sel.Method != "" {
		t.Fatal("--provision=never must not provision")
	}
	if refusal == nil || refusal.Reason == "" {
		t.Fatal("never must explain itself")
	}
}

func TestSelectWithExplicitAuthorization(t *testing.T) {
	cands := []Candidate{{
		Method:           MethodPackageManager,
		Requires:         []Authorization{AuthInstallScripts},
		Ownership:        OwnershipSystem,
		LifecycleScripts: true,
	}}
	sel, refusal := Select(cands, Policy{Mode: PolicySafe, Allow: []Authorization{AuthInstallScripts}})
	if refusal != nil {
		t.Fatalf("explicit authorization should unlock it: %+v", refusal)
	}
	if sel.Method != MethodPackageManager {
		t.Fatalf("selected %s", sel.Method)
	}
}

func TestAuthorizationIsSpecific(t *testing.T) {
	p := Policy{Mode: PolicySafe, Allow: []Authorization{AuthNetwork}}
	c := Candidate{Method: MethodSourceBuild, Requires: []Authorization{AuthSourceBuild}}
	if p.Grants(c) {
		t.Fatal("one authorization must not unlock a different risk class")
	}
}

func TestNoGenericYesAuthorization(t *testing.T) {
	// There is deliberately no generic "yes": every unlock is a named risk.
	for _, a := range []Authorization{AuthInstallScripts, AuthSystemPackageManager, AuthSourceBuild, AuthNetwork} {
		if a == "yes" || a == "all" {
			t.Fatal("a generic bypass authorization must not exist")
		}
	}
}

func TestRefusalExplainsAlternatives(t *testing.T) {
	_, refusal := Select([]Candidate{
		{Method: MethodPackageManager, Requires: []Authorization{AuthInstallScripts}, Ownership: OwnershipSystem, LifecycleScripts: true, Risks: []string{"runs arbitrary install scripts"}},
		{Method: MethodRelease, Ownership: OwnershipGrokinstall},
	}, Policy{Mode: PolicyNever})
	if refusal == nil {
		t.Fatal("expected refusal")
	}
	var buf bytes.Buffer
	refusal.Explain(&buf)
	out := buf.String()
	for _, want := range []string{"PROVISIONING BLOCKED", "allow-install-scripts", "Alternatives", "lifecycle scripts"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("refusal missing %q:\n%s", want, out)
		}
	}
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"safe", "never", "prompt", "SAFE"} {
		if _, ok := ParseMode(s); !ok {
			t.Fatalf("%q should be a valid policy", s)
		}
	}
	if _, ok := ParseMode("yolo"); ok {
		t.Fatal("unknown policies must be rejected")
	}
}

func TestUnresolvedIsNeverSelected(t *testing.T) {
	sel, _ := Select([]Candidate{{Method: MethodUnresolved}}, Policy{Mode: PolicySafe})
	if sel.Method == MethodUnresolved {
		t.Fatal("unresolved is not a provisioning route")
	}
}
