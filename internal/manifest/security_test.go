package manifest

import (
	"strings"
	"testing"
)

func TestContractRedactsSecrets(t *testing.T) {
	m := installed()
	m.Grokbot.UseWhen = "the user asks to configure GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123"
	m.Grokbot.DoNot = "never paste PASSWORD=hunter2hunter2 into the model"
	text := m.ContractText()
	if strings.Contains(text, "ghp_abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("a token leaked into the contract:\n%s", text)
	}
	if strings.Contains(text, "hunter2hunter2") {
		t.Fatalf("a password leaked into the contract:\n%s", text)
	}
}

func TestContractRedactsInputFieldDescriptions(t *testing.T) {
	m := installed()
	m.Input.Fields = []Field{{Name: "query", Type: "string", Description: "search; uses API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456"}}
	text := m.ContractText()
	if strings.Contains(text, "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("a key leaked through a field description:\n%s", text)
	}
}

func TestContractOmitsReceiptInternals(t *testing.T) {
	m := installed()
	m.Provenance.InstallID = "gi_secret"
	m.Provenance.ReceiptPath = "/very/internal/path/receipt.json"
	m.Provenance.Evidence = nil
	text := m.ContractText()
	if strings.Contains(text, "gi_secret") || strings.Contains(text, "receipt.json") {
		t.Fatalf("receipt internals leaked into the contract:\n%s", text)
	}
}

func TestContractOmitsSourcePath(t *testing.T) {
	m := installed()
	m.Source = "path:/Users/someone/private/project"
	text := m.ContractText()
	if strings.Contains(text, "/Users/someone") {
		t.Fatalf("a local path leaked into the contract:\n%s", text)
	}
}

func TestContractKeepsFailureRecoveryPointingAtARealCommand(t *testing.T) {
	m := installed()
	text := m.ContractText()
	if !strings.Contains(text, "grokinstall diagnose") {
		t.Fatalf("the contract must point at a diagnose command:\n%s", text)
	}
}
