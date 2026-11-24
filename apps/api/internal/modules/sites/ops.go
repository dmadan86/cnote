package sites

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

const maskReply = "Contact details can't be changed by chat while privacy masking is on."

func hasMask(v any) bool {
	switch x := v.(type) {
	case string:
		for _, marker := range []string{"[PHONE]", "[EMAIL]", "[GSTIN]", "[PAN]", "[AADHAAR]"} {
			if strings.Contains(x, marker) {
				return true
			}
		}
	case map[string]any:
		for _, v := range x {
			if hasMask(v) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if hasMask(v) {
				return true
			}
		}
	}
	return false
}

// maskCheck refuses a masked marker in a prop that this edit changes; props that already held one may stay.
func maskCheck(old, next map[string]any) error {
	for k, v := range next {
		if k != "id" && !reflect.DeepEqual(old[k], v) && hasMask(v) {
			return errors.New(maskReply)
		}
	}
	return nil
}

// Apply applies one edit to a copy of the document and validates the result.
func Apply(data json.RawMessage, tool string, input json.RawMessage) (json.RawMessage, error) {
	return applyWith(data, tool, input, nil)
}

// applyWith is Apply with the values add_page fills into a legal-page template (see templateSlots).
func applyWith(data json.RawMessage, tool string, input json.RawMessage, slots map[string]string) (json.RawMessage, error) {
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if err := Validate(data); err != nil {
		return nil, err
	}
	var in map[string]any
	if err := json.Unmarshal(input, &in); err != nil || in == nil {
		return nil, fmt.Errorf("invalid input")
	}
	embeds := embedState(d)
	var err error
	switch tool {
	case "update_block_props":
		err = updateBlockProps(d, in)
	case "add_block":
		err = addBlock(d, in)
	case "remove_block":
		err = removeBlock(d, in)
	case "move_block":
		err = moveBlock(d, in)
	case "set_page":
		err = setPage(d, in)
	case "add_page":
		err = addPage(d, in, slots)
	case "remove_page":
		err = removePage(d, in)
	case "set_content":
		err = setContent(d, in)
	case "set_theme":
		theme := d["root"].(map[string]any)["props"].(map[string]any)["theme"].(map[string]any)
		if err = maskCheck(theme, in); err == nil {
			for k, v := range in {
				theme[k] = v
			}
		}
	default:
		err = fmt.Errorf("unknown tool %s", tool)
	}
	if err == nil {
		err = normalizeEmbeds(d, embeds)
	}
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if err = Validate(out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- reading the document ----------

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// pageMaps are the extra pages in order.
func pageMaps(d map[string]any) []map[string]any {
	var out []map[string]any
	list, _ := d["pages"].([]any)
	for _, p := range list {
		if m := asMap(p); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func findPage(d map[string]any, slug string) map[string]any {
	for _, p := range pageMaps(d) {
		if p["slug"] == slug {
			return p
		}
	}
	return nil
}

// contentOf is the block list of a page; the empty slug is Home.
func contentOf(d map[string]any, slug string) []any {
	if slug == "" {
		c, _ := d["content"].([]any)
		return c
	}
	c, _ := findPage(d, slug)["content"].([]any)
	return c
}

func setContentOf(d map[string]any, slug string, c []any) {
	if c == nil {
		c = []any{}
	}
	if slug == "" {
		d["content"] = c
	} else {
		findPage(d, slug)["content"] = c
	}
}

// pageArg reads an optional page address: absent or empty is Home, anything else must be a page of the site.
func pageArg(d map[string]any, v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("page must be a page address")
	}
	if s != "" && findPage(d, s) == nil {
		return "", fmt.Errorf("unknown page %q", s)
	}
	return s, nil
}

// slugsOf returns the page addresses of every page, Home first as "".
func slugsOf(d map[string]any) []string {
	out := []string{""}
	for _, p := range pageMaps(d) {
		s, _ := p["slug"].(string)
		out = append(out, s)
	}
	return out
}

// locate finds a block by id on any page.
func locate(d map[string]any, id any) (slug string, i int, ok bool) {
	for _, s := range slugsOf(d) {
		for i, b := range contentOf(d, s) {
			if asMap(asMap(b)["props"])["id"] == id {
				return s, i, true
			}
		}
	}
	return "", 0, false
}

func allIDs(d map[string]any) map[string]bool {
	ids := map[string]bool{}
	for _, s := range slugsOf(d) {
		for _, b := range contentOf(d, s) {
			if id, ok := asMap(asMap(b)["props"])["id"].(string); ok {
				ids[id] = true
			}
		}
	}
	return ids
}

// freshID makes a block id the server chooses, never one in taken (which it updates).
func freshID(typ string, taken map[string]bool) (string, error) {
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		id := typ + "-" + hex.EncodeToString(b[:])
		if !taken[id] {
			taken[id] = true
			return id, nil
		}
	}
}

func index(v any, max int) (int, error) {
	n, ok := v.(float64)
	if !ok || n != float64(int(n)) || n < 0 || n > float64(max) {
		return 0, fmt.Errorf("invalid index")
	}
	return int(n), nil
}

// ---------- block operations ----------

func updateBlockProps(d, in map[string]any) error {
	slug, i, ok := locate(d, in["id"])
	if !ok {
		return fmt.Errorf("unknown block id")
	}
	props, ok := in["props"].(map[string]any)
	if !ok {
		return fmt.Errorf("props must be an object")
	}
	dst := asMap(asMap(contentOf(d, slug)[i])["props"])
	if _, has := props["id"]; has {
		return fmt.Errorf("id cannot be changed")
	}
	if err := maskCheck(dst, props); err != nil {
		return err
	}
	for k, v := range props {
		dst[k] = v
	}
	return nil
}

func addBlock(d, in map[string]any) error {
	slug, err := pageArg(d, in["page"])
	if err != nil {
		return err
	}
	typ, ok := in["type"].(string)
	if !ok {
		return fmt.Errorf("type required")
	}
	props, ok := in["props"].(map[string]any)
	if !ok {
		return fmt.Errorf("props must be an object")
	}
	if _, ok := props["id"]; ok {
		return fmt.Errorf("id is assigned automatically")
	}
	if hasMask(props) {
		return fmt.Errorf("%s", maskReply)
	}
	content := contentOf(d, slug)
	id, err := freshID(typ, allIDs(d))
	if err != nil {
		return err
	}
	props["id"] = id
	pos := len(content)
	if v, ok := in["index"]; ok {
		if pos, err = index(v, len(content)); err != nil {
			return err
		}
	}
	content = append(content, nil)
	copy(content[pos+1:], content[pos:])
	content[pos] = map[string]any{"type": typ, "props": props}
	setContentOf(d, slug, content)
	return nil
}

func removeBlock(d, in map[string]any) error {
	slug, i, ok := locate(d, in["id"])
	if !ok {
		return fmt.Errorf("unknown block id")
	}
	content := contentOf(d, slug)
	setContentOf(d, slug, append(content[:i], content[i+1:]...))
	return nil
}

func moveBlock(d, in map[string]any) error {
	slug, i, ok := locate(d, in["id"])
	if !ok {
		return fmt.Errorf("unknown block id")
	}
	content := contentOf(d, slug)
	pos, err := index(in["index"], len(content)-1)
	if err != nil {
		return err
	}
	b := content[i]
	content = append(content[:i], content[i+1:]...)
	content = append(content, nil)
	copy(content[pos+1:], content[pos:])
	content[pos] = b
	setContentOf(d, slug, content)
	return nil
}

// setContent replaces one page's blocks with the editor's list. A block keeps its id only when that id is on this page
// now with the same type; every other block gets an id from the server, so a client cannot choose them (design 17.11).
func setContent(d, in map[string]any) error {
	for k := range in {
		if k != "page" && k != "content" {
			return fmt.Errorf("unknown property %s", k)
		}
	}
	slug, err := pageArg(d, in["page"])
	if err != nil {
		return err
	}
	list, ok := in["content"].([]any)
	if !ok {
		return fmt.Errorf("content must be a list of blocks")
	}
	old := map[string]map[string]any{}
	for _, b := range contentOf(d, slug) {
		if id, ok := asMap(asMap(b)["props"])["id"].(string); ok {
			old[id] = asMap(b)
		}
	}
	taken := allIDs(d)
	used := map[string]bool{}
	next := make([]any, 0, len(list))
	for _, x := range list {
		m := asMap(x)
		typ, _ := m["type"].(string)
		src := asMap(m["props"])
		if typ == "" || src == nil {
			return fmt.Errorf("each block needs a type and props")
		}
		props := map[string]any{}
		for k, v := range src {
			props[k] = v
		}
		cand, _ := props["id"].(string)
		var before map[string]any
		if ob, ok := old[cand]; ok && !used[cand] && ob["type"] == typ {
			before = asMap(ob["props"])
		} else if used[cand] || taken[cand] || !regexp.MustCompile(`^`+regexp.QuoteMeta(typ)+`-[0-9a-f]{8}$`).MatchString(cand) {
			cand = ""
		}
		if err := maskCheck(before, props); err != nil {
			return err
		}
		if cand == "" {
			if cand, err = freshID(typ, taken); err != nil {
				return err
			}
		}
		used[cand] = true
		taken[cand] = true
		props["id"] = cand
		next = append(next, map[string]any{"type": typ, "props": props})
	}
	setContentOf(d, slug, next)
	return nil
}

// ---------- embeds ----------

// embedState remembers each Embed block's provider and ref, so only the ones an edit touches are normalised.
func embedState(d map[string]any) map[string]string {
	out := map[string]string{}
	eachBlock(d, func(b map[string]any) {
		if b["type"] == "Embed" {
			p := asMap(b["props"])
			out[fmt.Sprint(p["id"])] = fmt.Sprint(p["provider"], "\x00", p["source"])
		}
	})
	return out
}

// normalizeEmbeds turns a pasted link or iframe snippet into the stored ref (design 17.7) for new or changed Embed blocks.
func normalizeEmbeds(d map[string]any, before map[string]string) error {
	var err error
	eachBlock(d, func(b map[string]any) {
		if b["type"] != "Embed" || err != nil {
			return
		}
		p := asMap(b["props"])
		provider, _ := p["provider"].(string)
		value, ok := p["source"].(string)
		if !ok || before[fmt.Sprint(p["id"])] == fmt.Sprint(provider, "\x00", value) {
			return
		}
		norm, e := NormalizeEmbed(provider, value)
		if e != nil {
			err = fmt.Errorf("Embed: that is not a link this block can show. Use a YouTube, Google Maps, Google Forms or Calendly link")
			return
		}
		p["source"] = norm
	})
	return err
}
