package notes

import "github.com/cnote/api/internal/command"

// InsertNote is Rust's `insert_note` for other modules (billing): create a typed note and return its id.
func InsertNote(c *command.Ctx, spaceID, title string, content *string, types []string) (string, error) {
	r, err := insertNote(c, createNote{spaceID: spaceID, title: title, content: content, types: types})
	if err != nil {
		return "", err
	}
	return r.(Note).ID, nil
}

// AddRelation is Rust's `add_relation`.
func AddRelation(c *command.Ctx, from, to, kind string) error {
	return addRelation(c, from, to, kind, nil)
}
