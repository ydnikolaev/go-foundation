package agentdocs

import "fmt"

// Tool is the binary that ships, installs and reads the docs.
//
//   - ID is the marker namespace ("mytool"): PROVENANCE.md opens with
//     "<!-- <ID>-skill-install -->", and it is the name callers pass to Link
//     and UpsertPointer. It may differ from Name: a2a's binary is "a2a" and
//     its id is "a2ahub". Install refuses an empty ID.
//   - Name is the binary as a user types it ("mytool"); it is used in prose:
//     PROVENANCE.md's note and the workflow line.
//   - Version is the display version ("1.4.0 (local, 1a2b3c)"); it goes into
//     PROVENANCE.md and is never compared.
//   - Comparable is the version Installed compares ("1.4.0"), and "" for a dev
//     build.
//   - Source is where the tree comes from ("github.com/acme/mytool").
//   - Refresh is the command that reinstalls ("mytool skill install").
type Tool struct{ ID, Name, Version, Comparable, Source, Refresh string }

// WorkflowLine is the one line a verb's usage output prints to point at its
// walkthrough: "workflow: run `<name> docs <topic>` for the walkthrough".
func WorkflowLine(t Tool, topic string) string {
	return fmt.Sprintf("workflow: run `%s docs %s` for the walkthrough", t.Name, topic)
}
