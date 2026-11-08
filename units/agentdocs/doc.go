// Package agentdocs ships a CLI's agent documentation inside its binary and
// gets it in front of the agent working in a consuming repository.
//
// A tool embeds a tree of Markdown pages and a JSON manifest (schema
// "agentdocs-manifest/v1") that lists them: id, group, title, file, and the
// command paths each page documents. LoadManifest validates it; Topics,
// Resolve and Read serve `<tool> docs <topic>` straight from the embed;
// TopicFor maps a command path to its page and WorkflowLine prints the
// pointer a verb's usage carries.
//
// Install mirrors the tree into the repository under a PROVENANCE.md marker
// namespaced by Tool.ID,
// refusing a directory that is not its own; Installed compares the install's
// stamped comparable version with the running binary's for a doctor row, and
// InstallStatus.Severity grades it; Drift walks the install against the tree
// and names the missing, modified and unexpected files. Surfaces holds the verified agent-runtime
// rows (where each runtime finds a skill and which context file it reads),
// Detect finds the ones a repository uses, and Link points each one at the
// install, by relative symlink or a stub. UpsertPointer keeps one delimited
// block in a context file such as AGENTS.md. Stale names the generated files
// in a tree that no longer match what the generator writes.
//
// Every function that touches the disk takes the root the caller names and
// paths inside it, and reaches the disk only through an os.Root on that root:
// a symlink inside the root is followed, one that leaves it is refused.
//
// Content, CLI wiring, doctor wording and the reference generator stay with
// each tool; whether init or an explicit verb calls Install is the tool's
// choice.
//
// Origin: a2ahub skill/, internal/surface and internal/cli at 802fc17a.
package agentdocs
