package ai

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

const (
	chunkChars   = 800
	chunkOverlap = 100
	embedBatch   = 64
)

// ChunkText is cnote-ai chunk_text: windows of size runes cut at the last newline (else whitespace) in the
// window's second half, overlapping by overlap runes, trimmed, blanks dropped.
func ChunkText(text string, size, overlap int) []string {
	chars := []rune(text)
	out := []string{}
	start := 0
	for start < len(chars) {
		end := min(start+size, len(chars))
		if end < len(chars) {
			cut := -1
			for i := end - 1; i >= start+size/2; i-- {
				if chars[i] == '\n' {
					cut = i
					break
				}
			}
			if cut < 0 {
				for i := end - 1; i >= start+size/2; i-- {
					if unicode.IsSpace(chars[i]) {
						cut = i
						break
					}
				}
			}
			if cut >= 0 {
				end = cut + 1
			}
		}
		if p := strings.TrimSpace(string(chars[start:end])); p != "" {
			out = append(out, p)
		}
		if end >= len(chars) {
			break
		}
		start = max(end-overlap, start+1)
	}
	return out
}

// IndexJob is the ai.index_note job: replace the note's chunks for the current embedding model.
func IndexJob(c *command.Ctx, payload json.RawMessage) error {
	var p struct {
		NoteID string `json:"note_id"`
	}
	if json.Unmarshal(payload, &p) != nil || !validUUID(p.NoteID) {
		return command.ErrInvalid("ai.index_note needs note_id")
	}
	var title, content, freshness string
	var chunks []string
	err := c.Tx.QueryRow(c.Context, "select title, content_md, freshness from core.notes where id = $1::uuid and deleted_at is null", p.NoteID).Scan(&title, &content, &freshness)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if err == nil && freshness != "archived" {
		chunks = ChunkText(title+"\n\n"+notes.PlainText(content), chunkChars, chunkOverlap)
	}
	if len(chunks) == 0 {
		_, err := c.Tx.Exec(c.Context, "delete from ai.chunks where note_id = $1::uuid", p.NoteID)
		return err
	}
	var vectors [][]float32
	model := ""
	for i := 0; i < len(chunks); i += embedBatch {
		m, v, err := embed(c, "index", chunks[i:min(i+embedBatch, len(chunks))])
		if err != nil {
			return err
		}
		model = m
		vectors = append(vectors, v...)
	}
	lits := make([]string, len(vectors))
	ords := make([]int32, len(chunks))
	for i, v := range vectors {
		lits[i] = VecLiteral(v)
		ords[i] = int32(i)
	}
	if _, err := c.Tx.Exec(c.Context, "delete from ai.chunks where note_id = $1::uuid and model = $2", p.NoteID, model); err != nil {
		return err
	}
	_, err = c.Tx.Exec(c.Context, `insert into ai.chunks (workspace_id, note_id, model, ordinal, content, embedding)
		select $1::uuid, $2::uuid, $3, o, c, e::vector from unnest($4::int[], $5::text[], $6::text[]) as t(o, c, e)`,
		c.WorkspaceID(), p.NoteID, model, ords, chunks, lits)
	return err
}

// ProposeEdit asks for a revised page through Complete: the workspace's key, redaction, audit and metering apply.
func ProposeEdit(c *command.Ctx, msgs []Message, maxTokens uint32) (Completion, error) {
	return complete(c, "propose_edit", msgs, maxTokens)
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
