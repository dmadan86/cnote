package sites

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
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

// Apply applies one edit to a copy of the document and validates the result.
func Apply(data json.RawMessage, tool string, input json.RawMessage) (json.RawMessage, error) {
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
	content := d["content"].([]any)
	find := func(id any) int {
		for i, b := range content {
			if b.(map[string]any)["props"].(map[string]any)["id"] == id {
				return i
			}
		}
		return -1
	}
	index := func(v any, max int) (int, error) {
		n, ok := v.(float64)
		if !ok || n != float64(int(n)) || n < 0 || n > float64(max) {
			return 0, fmt.Errorf("invalid index")
		}
		return int(n), nil
	}
	switch tool {
	case "update_block_props":
		i := find(in["id"])
		if i < 0 {
			return nil, fmt.Errorf("unknown block id")
		}
		props, ok := in["props"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("props must be an object")
		}
		dst := content[i].(map[string]any)["props"].(map[string]any)
		for k, v := range props {
			if k == "id" {
				return nil, fmt.Errorf("id cannot be changed")
			}
			if !reflect.DeepEqual(dst[k], v) && hasMask(v) {
				return nil, fmt.Errorf("%s", maskReply)
			}
			dst[k] = v
		}
	case "add_block":
		typ, ok := in["type"].(string)
		if !ok {
			return nil, fmt.Errorf("type required")
		}
		props, ok := in["props"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("props must be an object")
		}
		if _, ok := props["id"]; ok {
			return nil, fmt.Errorf("id is assigned automatically")
		}
		if hasMask(props) {
			return nil, fmt.Errorf("%s", maskReply)
		}
		for {
			var b [4]byte
			if _, err := rand.Read(b[:]); err != nil {
				return nil, err
			}
			id := typ + "-" + hex.EncodeToString(b[:])
			if find(id) < 0 {
				props["id"] = id
				break
			}
		}
		pos := len(content)
		if v, ok := in["index"]; ok {
			var err error
			pos, err = index(v, len(content))
			if err != nil {
				return nil, err
			}
		}
		content = append(content, nil)
		copy(content[pos+1:], content[pos:])
		content[pos] = map[string]any{"type": typ, "props": props}
	case "remove_block":
		i := find(in["id"])
		if i < 0 {
			return nil, fmt.Errorf("unknown block id")
		}
		content = append(content[:i], content[i+1:]...)
	case "move_block":
		i := find(in["id"])
		if i < 0 {
			return nil, fmt.Errorf("unknown block id")
		}
		pos, err := index(in["index"], len(content)-1)
		if err != nil {
			return nil, err
		}
		b := content[i]
		content = append(content[:i], content[i+1:]...)
		content = append(content, nil)
		copy(content[pos+1:], content[pos:])
		content[pos] = b
	case "set_page":
		root := d["root"].(map[string]any)["props"].(map[string]any)
		for k, v := range in {
			if k != "title" && k != "description" {
				return nil, fmt.Errorf("unknown page property")
			}
			if !reflect.DeepEqual(root[k], v) && hasMask(v) {
				return nil, fmt.Errorf("%s", maskReply)
			}
			root[k] = v
		}
	case "set_theme":
		theme := d["root"].(map[string]any)["props"].(map[string]any)["theme"].(map[string]any)
		for k, v := range in {
			if !reflect.DeepEqual(theme[k], v) && hasMask(v) {
				return nil, fmt.Errorf("%s", maskReply)
			}
			theme[k] = v
		}
	default:
		return nil, fmt.Errorf("unknown tool %s", tool)
	}
	d["content"] = content
	out, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if err = Validate(out); err != nil {
		return nil, err
	}
	return out, nil
}
