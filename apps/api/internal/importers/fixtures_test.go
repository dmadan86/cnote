package importers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Fixtures for the differential test: representative, unicode, malformed and large inputs per source.

const jiraEdge = `{"issues":[
 {"key":"U-1","self":"https://acme.atlassian.net/rest/api/3/issue/1","fields":{
   "summary":"  Ünïcödé 日本語 🚀 ΣΑΣ İstanbul  ",
   "description":{"type":"doc","content":[
     {"type":"heading","attrs":{"level":9},"content":[{"type":"text","text":"Big"}]},
     {"type":"heading","attrs":{"level":0},"content":[{"type":"text","text":"Zero"}]},
     {"type":"heading","attrs":{"level":2.0},"content":[{"type":"text","text":"Float"}]},
     {"type":"paragraph","content":[{"type":"text","text":"héllo "},{"type":"mention","attrs":{"text":"@ada"}},{"type":"mention","attrs":{}},{"type":"hardBreak"},{"type":"text","text":"wörld"}]},
     {"type":"orderedList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},{"type":"listItem","content":[{"type":"text","text":"two"}]}]},
     {"type":"codeBlock","content":[{"type":"text","text":"let x = 1;"}]},
     {"type":"weird","content":[{"type":"text","text":"w"}]},
     "bare string", 42, null
   ]},
   "issuetype":{"name":"Épic Ü/Δ"},
   "status":{"name":"Fertig İşlem Done","statusCategory":{"key":"done"}},
   "priority":{"name":"  BLOCKER "},
   "assignee":{"displayName":"Zoë","emailAddress":"ADA@EXAMPLE.COM"},
   "reporter":{"displayName":"Rép","emailAddress":""},
   "labels":["ünï","日本","",5,null],
   "components":[{"name":"Ünï"},{"name":3},{"name":"Zed"}],
   "fixVersions":[{"name":"v1 ü","released":"yes","releaseDate":"2024-3-5"},{"name":"v2"}],
   "customfield_10016": 2.5,
   "duedate":"2024-03-05T00:00:00",
   "resolutiondate":"2024-03-01 10:00:00+05:30",
   "created":"2024-03-01T10:00:00.5+0530",
   "customfield_10020":[{"name":"S ü","state":"weird","startDate":"2024-3-1"},{"name":"S2","state":"active","boardId":1,"startDate":"2024-03-01T00:00:00.000Z","endDate":"2024-03-15"}],
   "customfield_10021":[{"name":"x","state":"y"}],
   "customfield_10022":[],"customfield_10023":null,"customfield_10024":"s","customfield_10025":{"a":1},
   "zzz":["a"],"environment":"ignored","statuscategorychangedate":"x",
   "issuelinks":[{"type":{"name":"BLOCKS ü"},"outwardIssue":{"key":"U-2"}},{"type":{"name":"Relates"},"inwardIssue":{"key":"U-2"}},{"type":{"name":"Relates"},"outwardIssue":{"key":"U-2"}},{"type":{},"outwardIssue":{"key":""}}],
   "comment":{"comments":[{"id":"1","author":{"displayName":"A","emailAddress":"ada@example.com"},"created":"2024-03-02T00:00:00Z","body":"hi 🚀"},{"id":"2","body":{"type":"doc","content":[]}},{"author":{}}]},
   "attachment":[{"content":"https://x/a","filename":"é.png","size":1.5},{"content":"https://x/b","size":-3},{"filename":"nocontent"}]
 },"changelog":{"histories":[
   {"id":"h1","created":"2024-03-01T01:00:00Z","author":{"displayName":"Ü"},"items":[{"field":"status","fromString":"To Do","toString":"Done"},{"field":"assignee","fromString":null,"toString":"Zoë"},{"field":null}]},
   {"created":"bad","items":[{"field":"x","fromString":"","toString":"y"}]}]}},
 {"key":"U-2","fields":{"summary":"Child","parent":{"key":"U-1"},"customfield_10014":"U-1","status":{"name":"X"}}},
 {"key":"U-3","fields":{"summary":"Epic link","customfield_10014":"U-1","priority":{"name":"nope"}}},
 {"key":"U-4","fields":null},{"key":"U-5"},{"fields":{"summary":"nokey"}},{"key":"U-6","fields":{}},
 {"key":"U-7","fields":{"summary":"dup comment ids","comment":{"comments":[{"id":"1","body":"a"},{"id":"1","body":"b"}]}}},
 {"key":"U-8","fields":{"summary":"self parent","parent":{"key":"U-8"},"issuelinks":[{"type":{"name":"Relates"},"outwardIssue":{"key":"U-8"}}]}},
 "notanobject",null
],
"filters":[
 {"id":"f1","name":"Ünï filter","jql":"ÉWas = 1"},{"name":"Slug me","jql":"status was Done"},{"name":"tilde","jql":"text ~ y"},
 {"id":"f1","name":"dup id","jql":"project = X"},{"name":"","jql":"a"},{"name":"caps","jql":"status in (A) AND NOT (b) or (c) CF[1]"},
 {"name":"fn","jql":"x = now()"},{"name":"Sigma","jql":"ΣWAS and during"},{"name":"Ünï slug","jql":"waſ"},{"name":"nbsp call","jql":"foo (1)"}
],
"automation_rules":[
 {"name":"R1","trigger":{"type":"x","n":1.50,"m":1e2,"big":12345678901234567890,"neg":-0.0,"arr":[1,2.0,3e-7],"ü":"é"},"conditions":[{"a":1.0}],"actions":[]},
 {"name":"R2"},{"name":"R1","trigger":null},{"name":""},{"trigger":{}}
]}`

func jiraLarge(n int) string {
	issues := make([]any, 0, n)
	for i := 1; i <= n; i++ {
		f := map[string]any{
			"summary":     fmt.Sprintf("Issue %d ünï", i),
			"description": fmt.Sprintf("Body of %d", i),
			"issuetype":   map[string]any{"name": []string{"Story", "Bug", "Task"}[i%3]},
			"status": map[string]any{"name": []string{"To Do", "In Progress", "Done"}[i%3],
				"statusCategory": map[string]any{"key": []string{"new", "indeterminate", "done"}[i%3]}},
			"priority": map[string]any{"name": []string{"Low", "High", "Medium", "Highest"}[i%4]},
			"labels":   []string{fmt.Sprintf("l%d", i%5)},
			"created":  fmt.Sprintf("2024-01-%02dT10:00:00.000+0000", i%28+1),
		}
		if i%3 == 0 {
			f["assignee"] = map[string]any{"displayName": "Ada", "emailAddress": "ada@example.com"}
		}
		if i > 10 {
			f["parent"] = map[string]any{"key": fmt.Sprintf("L-%d", i/10)}
		}
		links := []any{}
		if i > 1 {
			links = append(links, map[string]any{"type": map[string]any{"name": "Blocks"}, "outwardIssue": map[string]any{"key": fmt.Sprintf("L-%d", i-1)}})
		}
		if i < n {
			links = append(links, map[string]any{"type": map[string]any{"name": "Relates"}, "inwardIssue": map[string]any{"key": fmt.Sprintf("L-%d", i+1)}})
		}
		f["issuelinks"] = links
		if i%7 == 0 {
			f["comment"] = map[string]any{"comments": []any{map[string]any{"id": fmt.Sprint(i), "body": "c", "created": "2024-02-01T00:00:00Z",
				"author": map[string]any{"displayName": "Ada", "emailAddress": "ada@example.com"}}}}
		}
		if i%13 == 0 {
			f["attachment"] = []any{map[string]any{"content": fmt.Sprintf("https://x/%d", i), "size": i}}
		}
		if i%11 == 0 {
			f["customfield_10020"] = []any{map[string]any{"name": fmt.Sprintf("Sprint %d", i%4), "state": "active", "boardId": 1}}
		}
		issue := map[string]any{"key": fmt.Sprintf("L-%d", i), "self": "https://acme.atlassian.net/rest/api/3/issue/1", "fields": f}
		if i%5 == 0 {
			issue["changelog"] = map[string]any{"histories": []any{map[string]any{"id": fmt.Sprint(i), "created": "2024-02-02T00:00:00Z",
				"items": []any{map[string]any{"field": "status", "fromString": "To Do", "toString": "Done"}}}}}
		}
		issues = append(issues, issue)
	}
	b, _ := json.Marshal(map[string]any{"issues": issues})
	return string(b)
}

const trelloEdge = `{"lists":[{"id":"l1","name":"Backlog"},{"id":"l2","name":"Doing ünï"},{"id":"l3","name":"Done ✅"},{"id":"l4"},{"name":"noid"},{"id":"l5","name":"Review"},{"id":"l1","name":"Backlog 2"}],
"cards":[
 {"id":"c1","name":"  Ünï card 🚀 ","desc":"  desc  ","idList":"l2","labels":[{"name":"ü"},{"color":"red"},{"name":""}],"due":"2024-3-5T00:00:00Z","shortUrl":"https://t/c1"},
 {"id":"c2","name":"Done card","idList":"l3","dateLastActivity":"2024-04-01T10:00:00.000Z","url":"https://t/c2","shortUrl":"https://t/s"},
 {"id":"c3","name":"Closed","closed":true},{"id":"c4","name":"Closed 2","closed":true,"idList":"l1"},
 {"name":"no id"},{"id":"c5"},{"id":5,"name":"numeric id"},
 {"id":"c6","name":"Unknown list","idList":"zzz"},{"id":"c7","name":"Review","idList":"l5","due":"bad"},
 {"id":"c1","name":"dup of c1","idList":"l1"},{"id":"c8","name":"l4 no name","idList":"l4"},
 {"id":"c9","name":"Closed false","closed":false,"idList":"l1","desc":""}],
"checklists":[{"idCard":"c1","name":"List ü","checkItems":[{"name":"a","state":"complete"},{"state":"incomplete"},{"name":"b"}]},{"idCard":"c1","checkItems":[]},{"name":"orphan","checkItems":[]},{"idCard":"c9","name":"C9","checkItems":[{"name":"x","state":"complete"}]}],
"actions":[
 {"id":"a1","type":"commentCard","date":"2024-04-02T10:00:00.000Z","memberCreator":{"fullName":"Évé"},"data":{"text":"Ünï comment","card":{"id":"c1"}}},
 {"id":"a2","type":"commentCard","data":{"card":{"id":"c1"}}},
 {"type":"commentCard","date":"bad","data":{"text":"no id","card":{"id":"c2"}}},
 {"id":"a3","type":"updateCard","date":"2024-04-03T10:00:00.000Z","data":{"card":{"id":"c1"},"listBefore":{"name":"Backlog"},"listAfter":{"name":"Doing ünï"}}},
 {"id":"a4","type":"updateCard","data":{"card":{"id":"c1"},"listAfter":{"name":null}}},
 {"id":"a5","type":"updateCard","data":{"card":{"id":"c1"},"old":{"name":"x"}}},
 {"id":"a6","type":"updateCard","data":{"card":{"id":"c2"},"listBefore":{"name":"Doing"},"listAfter":{"name":"Done"}}},
 {"id":"a7","type":"commentCard","data":{"text":"ghost","card":{"id":"nope"}}},
 {"id":"a8","type":"createCard","data":{"card":{"id":"c1"}}},{"id":"a9","type":"commentCard","data":{"text":"x"}}]}`

func trelloLarge(n int) string {
	var cards, actions []string
	for i := 1; i <= n; i++ {
		cards = append(cards, fmt.Sprintf(`{"id":"c%d","name":"Card %d ü","desc":"d%d","idList":"l%d","labels":[{"name":"x%d"}]}`, i, i, i, i%3+1, i%4))
		if i%5 == 0 {
			actions = append(actions, fmt.Sprintf(`{"id":"a%d","type":"commentCard","date":"2024-04-02T10:00:00Z","data":{"text":"t%d","card":{"id":"c%d"}}}`, i, i, i))
		}
		if i%6 == 0 {
			actions = append(actions, fmt.Sprintf(`{"id":"m%d","type":"updateCard","date":"2024-04-02T10:00:00Z","data":{"card":{"id":"c%d"},"listBefore":{"name":"Backlog"},"listAfter":{"name":"Done"}}}`, i, i))
		}
	}
	return `{"lists":[{"id":"l1","name":"Backlog"},{"id":"l2","name":"In progress"},{"id":"l3","name":"Done"}],"cards":[` +
		strings.Join(cards, ",") + `],"actions":[` + strings.Join(actions, ",") + `]}`
}

const confluenceHTML = `<html><head><title>Ünï Page 🚀</title><style>p{color:red}</style></head><body>
<h1>Head &amp; &lt;b&gt; &#x1F600; &#65; &#+66; &#xD800; &#1114112; &bogus; &nbsp;x &amp</h1>
<!-- comment --><?php x ?><!DOCTYPE x>
<p>one<p>two <b>bold<i>both</b> tail</i> <del>d</del><s>s</s><code>c</code> <a href='u?a=1&amp;b=2'>link</a> <a>nolink</a> <a href="x"></a> <img src="i.png" alt="A &amp; B"><br/>after br<br>
<UL><LI>a<LI>b<ul><li>n1</li><li>n2<ol><li>deep</li></ol></li></ul></UL>
<ol><li>1</li><li><p>para in li</p></li></ol>
<pre class="x"><code class="lang-x language-go">fmt.Println("hi")
</code></pre>
<pre>plain<br>pre &lt;tag&gt;</pre>
<blockquote><p>q1</p><p>q2</p><blockquote><p>nested</p></blockquote></blockquote>
<table><thead><tr><th>A|B</th><th>C</th></tr></thead><tbody><tr><td>1</td></tr><tr><td>x<br>y</td><td>2</td><td>3</td></tr></tbody></table>
<table></table><hr>
<ac:structured-macro ac:name="info"><ac:rich-text-body><p>info body</p><ac:structured-macro ac:name="warning"><p>w</p></ac:structured-macro></ac:rich-text-body></ac:structured-macro>
<ac:structured-macro ac:name="code"><ac:parameter ac:name="title">T</ac:parameter><ac:plain-text-body><![CDATA[a < b && c
]]></ac:plain-text-body></ac:structured-macro>
<p>inline <ac:structured-macro ac:name="status"><ac:parameter ac:name="title">DONE</ac:parameter></ac:structured-macro> macro</p>
<div><span>span text</span> loose text <section>sec</section></div>
unclosed <b>bold
<script>alert("</p>")</script><SCRIPT>x</SCRIPT> after
<a href=unquoted data-x = "y">u</a> <p class=>x</p> <input type=text> <   > < b>
<p>ünï — “quotes” 日本語 İ</p>
</body></html>`

func pagesJSON(pages ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"pages": pages})
	return string(b)
}

func confluenceEdge() string {
	return pagesJSON(
		map[string]any{"id": "p1", "title": "Ünï root 🚀", "body": confluenceHTML, "author": "Évé", "created": "2024-5-6T07:08:09+0100", "url": "https://c/p1"},
		map[string]any{"id": "p2", "title": "Child", "parent_id": "p1", "html": "<p>child</p>", "body": "<p>ignored</p>"},
		map[string]any{"id": "p3", "title": "Orphan", "parent_id": "nope", "body": "<h2>x</h2>"},
		map[string]any{"id": "p4", "title": "Self", "parent_id": "p4", "body": ""},
		map[string]any{"id": "p5", "title": "Cycle a", "parent_id": "p6", "body": "<p>a</p>"},
		map[string]any{"id": "p6", "title": "Cycle b", "parent_id": "p5", "body": "<p>b</p>", "comments": []any{
			map[string]any{"author": "x", "body": "no id"}, map[string]any{"id": "k", "body": "with id", "created": "2024-01-01T00:00:00Z"}, map[string]any{}}},
		map[string]any{"title": "no id"}, map[string]any{"id": "x"}, map[string]any{"id": 7, "title": "numeric id"},
		map[string]any{"id": "p7", "title": "Markdown wins", "markdown": "# Markdown wins\n\ntext", "html": "<p>html</p>"},
		map[string]any{"id": "p8", "title": "T", "markdown": "# TT\n\nprefix of other title"},
	)
}

func notionEdge() string {
	return pagesJSON(
		map[string]any{"id": "n1", "title": "Ünï notion", "markdown": "# Ünï notion\r\n\r\nWelcome ü\r\n- a\r\n", "author": "Zoë"},
		map[string]any{"id": "n2", "title": "Sub", "parent_id": "n1", "markdown": "no heading here"},
		map[string]any{"id": "n3", "title": "Empty md", "markdown": ""},
		map[string]any{"id": "n4", "title": "Html in notion", "html": "<ac:structured-macro ac:name=\"toc\"/><p>x</p>"},
		map[string]any{"id": "n5", "title": "Ünï notion", "markdown": "# Ünï notion"},
	)
}

const csvEdge = "Ref,Summary,Details,State,Severity,Owner,Tags,Type,Parent,Link,Team,Team,Notes\r\n" +
	"R1,Ünï 日本語 🚀,\"multi\nline \"\"quoted\"\" text\",In Progress ünï,CRITICAL,ADA@EXAMPLE.COM,\"a, b,,c ,d\",Bug Ü,,https://x/1,T1,T2,\"note\"\r\n" +
	"R2,  Padded title  ,  ,Done ✅, p1 ,ada@example.com,,,R1,,,,\r\n" +
	"\r\n" +
	"R3,Short row\n" +
	"R4,Long row,d,s,low,o,t,k,R1,l,x,y,z,extra,more\n" +
	",No ref title,,review,weird,,,,,,,,\n" +
	"R6,,no title,,,,,,,,,,\n" +
	"R1,Duplicate ref,,Closed,,,,,,,,,\n" +
	"R7,Bare \"quote in field,x\n" +
	"R8,\"\"\"q\"\" x\",\"after\"quote,tail\n" +
	"R9,Trailing comma,,,,,,,,,,,\n" +
	"R10,Tab\tin title,,\"started\",,,\"\",,R404,,,,\n" +
	"R11,Last row no newline,,,,,,,,,,,\n"

func csvLarge(n int) string {
	var b strings.Builder
	b.WriteString("id,title,desc,status,priority,owner,tags,parent,extra\n")
	for i := 1; i <= n; i++ {
		par := ""
		if i > 10 {
			par = fmt.Sprintf("r%d", i/10)
		}
		fmt.Fprintf(&b, "r%d,Row %d ünï,\"d,%d\",%s,%s,%s,\"t%d,u\",%s,e%d\n", i, i,
			i, []string{"todo", "doing", "done"}[i%3], []string{"low", "high", "p0"}[i%3], map[bool]string{true: "ada@example.com"}[i%4 == 0], i%5, par, i)
	}
	return b.String()
}

func confluenceLarge(n int) string {
	pages := make([]map[string]any, 0, n)
	for i := 1; i <= n; i++ {
		p := map[string]any{"id": fmt.Sprint(i), "title": fmt.Sprintf("Page %d ü", i), "body": fmt.Sprintf("<h1>P%d</h1><p>text &amp; %d</p><ul><li>a</li></ul>", i, i)}
		if i > 5 {
			p["parent_id"] = fmt.Sprint(i / 5)
		}
		if i%9 == 0 {
			p["comments"] = []any{map[string]any{"id": "c", "body": "hi"}}
		}
		pages = append(pages, p)
	}
	return pagesJSON(pages...)
}

var allMapping = map[string]any{"id": "Ref", "title": "Summary", "description": "Details", "status": "State", "priority": "Severity",
	"assignee_email": "Owner", "labels": "Tags", "item_type": "Type", "parent": "Parent", "url": "Link"}
