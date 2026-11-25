package importers

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// Minimal shared XML fragments
// ---------------------------------------------------------------------------

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml"  ContentType="application/xml"/>
  <Override PartName="/word/document.xml"
            ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

const contentTypesMacroXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml"  ContentType="application/xml"/>
  <Default Extension="bin"  ContentType="application/vnd.ms-office.activeX+xml"/>
  <Override PartName="/word/document.xml"
            ContentType="application/vnd.ms-word.document.macroEnabled.main+xml"/>
  <Override PartName="/word/vbaProject.bin"
            ContentType="application/vnd.ms-office.vbaProject"/>
</Types>`

const rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
    Target="word/document.xml"/>
</Relationships>`

const docRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`

// addFile is a helper that writes a named entry into a zip.Writer.
func addFile(zw *zip.Writer, name, content string) {
	w, err := zw.Create(name)
	if err != nil {
		panic(err)
	}
	if _, err = io.WriteString(w, content); err != nil {
		panic(err)
	}
}

// addBytes is like addFile but takes a []byte payload.
func addBytes(zw *zip.Writer, name string, data []byte) {
	w, err := zw.Create(name)
	if err != nil {
		panic(err)
	}
	if _, err = w.Write(data); err != nil {
		panic(err)
	}
}

// newDocx returns a fresh in-memory zip.Writer and the underlying buffer.
func newDocx() (*bytes.Buffer, *zip.Writer) {
	buf := new(bytes.Buffer)
	return buf, zip.NewWriter(buf)
}

// sealDocx writes the standard package skeleton (content-types, root rels) and
// closes the writer, returning the finished []byte.
func sealDocx(buf *bytes.Buffer, zw *zip.Writer, contentTypes, docRels string) []byte {
	addFile(zw, "[Content_Types].xml", contentTypes)
	addFile(zw, "_rels/.rels", rootRelsXML)
	addFile(zw, "word/_rels/document.xml.rels", docRels)
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// docxSimple returns a minimal .docx with a title paragraph, two body
// paragraphs, one bold run and one italic run. Useful as a baseline import
// test for ordinary prose documents.
func docxSimple() []byte {
	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Title"/></w:pPr>
      <w:r><w:t>Simple Document Title</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:t>First paragraph of body text.</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:rPr><w:b/></w:rPr><w:t>Bold run.</w:t></w:r>
      <w:r><w:rPr><w:i/></w:rPr><w:t xml:space="preserve"> Italic run.</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	return sealDocx(buf, zw, contentTypesXML, docRelsXML)
}

// docxRich returns a .docx that exercises headings (levels 1–3), bullet and
// numbered lists, a 3×3 table, a hyperlink to https://example.com, and an
// inline image (word/media/image1.png with a minimal valid PNG) together with
// its relationship entry. Useful for testing rich-content importers.
func docxRich() []byte {
	// Minimal 1×1 red pixel PNG (67 bytes).
	minPNG := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // signature
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1×1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // 8-bit RGB, crc
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, // IDAT length + type
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, // compressed scanline
		0x00, 0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, // crc
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, // IEND length + type
		0x44, 0xae, 0x42, 0x60, 0x82, // IEND crc
	}

	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Heading One</w:t></w:r></w:p>
    <w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Heading Two</w:t></w:r></w:p>
    <w:p><w:pPr><w:pStyle w:val="Heading3"/></w:pPr><w:r><w:t>Heading Three</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>Bullet item one</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>Bullet item two</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr>
      <w:r><w:t>Numbered item one</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr>
      <w:r><w:t>Numbered item two</w:t></w:r></w:p>
    <w:tbl>
      <w:tr><w:tc><w:p><w:r><w:t>R1C1</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>R1C2</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>R1C3</w:t></w:r></w:p></w:tc></w:tr>
      <w:tr><w:tc><w:p><w:r><w:t>R2C1</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>R2C2</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>R2C3</w:t></w:r></w:p></w:tc></w:tr>
      <w:tr><w:tc><w:p><w:r><w:t>R3C1</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>R3C2</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>R3C3</w:t></w:r></w:p></w:tc></w:tr>
    </w:tbl>
    <w:p>
      <w:hyperlink r:id="rIdLink" w:history="1">
        <w:r><w:t>Visit example</w:t></w:r>
      </w:hyperlink>
    </w:p>
    <w:p>
      <w:r>
        <w:drawing>
          <wp:inline><a:graphic><a:graphicData
            uri="http://schemas.openxmlformats.org/drawingml/2006/picture">
            <pic:pic><pic:nvPicPr><pic:cNvPr id="1" name="image1.png"/><pic:cNvPicPr/></pic:nvPicPr>
              <pic:blipFill><a:blip r:embed="rIdImg"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>
              <pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="1" cy="1"/></a:xfrm>
                <a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>
            </pic:pic>
          </a:graphicData></a:graphic></wp:inline>
        </w:drawing>
      </w:r>
    </w:p>
  </w:body>
</w:document>`

	const richRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdLink"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
    Target="https://example.com" TargetMode="External"/>
  <Relationship Id="rIdImg"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
    Target="media/image1.png"/>
</Relationships>`

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	addBytes(zw, "word/media/image1.png", minPNG)
	addFile(zw, "[Content_Types].xml", contentTypesXML)
	addFile(zw, "_rels/.rels", rootRelsXML)
	addFile(zw, "word/_rels/document.xml.rels", richRels)
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// docxExternalImage returns a .docx whose relationship file contains an image
// relationship with TargetMode="External" pointing to an http URL. Used to
// test that the importer does not attempt to fetch or embed remote images.
func docxExternalImage() []byte {
	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>External image reference.</w:t></w:r></w:p></w:body>
</w:document>`

	const rels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdExtImg"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
    Target="http://example.com/x.png" TargetMode="External"/>
</Relationships>`

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	addFile(zw, "[Content_Types].xml", contentTypesXML)
	addFile(zw, "_rels/.rels", rootRelsXML)
	addFile(zw, "word/_rels/document.xml.rels", rels)
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// docxJavascriptLink returns a .docx whose relationship file contains a
// hyperlink with a javascript: scheme URI. Used to verify that the importer
// sanitises or rejects dangerous link targets and does not emit script URLs.
func docxJavascriptLink() []byte {
	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p>
      <w:hyperlink r:id="rIdJs" w:history="1">
        <w:r><w:t>Click me</w:t></w:r>
      </w:hyperlink>
    </w:p>
  </w:body>
</w:document>`

	const rels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdJs"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
    Target="javascript:alert(1)" TargetMode="External"/>
</Relationships>`

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	addFile(zw, "[Content_Types].xml", contentTypesXML)
	addFile(zw, "_rels/.rels", rootRelsXML)
	addFile(zw, "word/_rels/document.xml.rels", rels)
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// docxDeepNesting returns a .docx whose word/document.xml body wraps a single
// text run inside 200 levels of nested <w:p> elements. Used to verify that the
// importer does not stack-overflow or hang on pathologically deep XML trees.
func docxDeepNesting() []byte {
	var sb bytes.Buffer
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n")
	sb.WriteString("<w:document xmlns:w=\"http://schemas.openxmlformats.org/wordprocessingml/2006/main\">\n")
	sb.WriteString("  <w:body>\n")
	const depth = 200
	for i := 0; i < depth; i++ {
		sb.WriteString("<w:p>")
	}
	sb.WriteString("<w:r><w:t>deep</w:t></w:r>")
	for i := 0; i < depth; i++ {
		sb.WriteString("</w:p>")
	}
	sb.WriteString("\n  </w:body>\n</w:document>")

	buf, zw := newDocx()
	addBytes(zw, "word/document.xml", sb.Bytes())
	return sealDocx(buf, zw, contentTypesXML, docRelsXML)
}

// docxZipBomb returns a .docx (valid zip) that contains one entry whose
// uncompressed size is 60 MB (zeros compressed with Deflate). Used to verify
// that the importer enforces a decompression-size limit and does not exhaust
// memory when processing adversarial archives.
func docxZipBomb() []byte {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Use a raw Deflate-compressed header so we can store 60 MB of zeros
	// without reading 60 MB into this process during test compilation.
	const uncompressedSize = 60 * 1024 * 1024 // 60 MiB

	fh := &zip.FileHeader{
		Name:   "word/document.xml",
		Method: zip.Deflate,
	}
	w, err := zw.CreateHeader(fh)
	if err != nil {
		panic(err)
	}
	fw, err := flate.NewWriter(w, flate.BestCompression)
	if err != nil {
		panic(err)
	}
	zeros := make([]byte, 32*1024)
	remaining := uncompressedSize
	for remaining > 0 {
		n := remaining
		if n > len(zeros) {
			n = len(zeros)
		}
		if _, err = fw.Write(zeros[:n]); err != nil {
			panic(err)
		}
		remaining -= n
	}
	if err = fw.Close(); err != nil {
		panic(err)
	}

	addFile(zw, "[Content_Types].xml", contentTypesXML)
	addFile(zw, "_rels/.rels", rootRelsXML)
	addFile(zw, "word/_rels/document.xml.rels", docRelsXML)
	if err = zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// docxManyEntries returns a .docx that contains 1 500 small zip entries in
// addition to the standard package parts. Used to verify that the importer
// handles archives with a very large entry count without excessive memory
// allocation or time.
func docxManyEntries() []byte {
	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>many entries</w:t></w:r></w:p></w:body>
</w:document>`

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	for i := 0; i < 1500; i++ {
		name := bytes.NewBufferString("word/extra/")
		// build a unique name without fmt to stay stdlib-only
		name.WriteString(itoa(i))
		name.WriteString(".txt")
		addFile(zw, name.String(), "x")
	}
	return sealDocx(buf, zw, contentTypesXML, docRelsXML)
}

// itoa converts a non-negative integer to its decimal string representation
// without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// docxMacro returns a .docx (using the macroEnabled content type) that
// contains a word/vbaProject.bin entry. Used to verify that the importer
// detects and either rejects or sanitises macro-enabled documents.
func docxMacro() []byte {
	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>Macro document.</w:t></w:r></w:p></w:body>
</w:document>`

	// Minimal stub for a VBA project binary (not a real compound document,
	// just enough bytes for the importer to detect the entry's presence).
	vbaStub := []byte("VBA_PROJECT_STUB")

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	addBytes(zw, "word/vbaProject.bin", vbaStub)
	return sealDocx(buf, zw, contentTypesMacroXML, docRelsXML)
}

// docxNotAZip returns random-looking bytes that are not a valid zip archive.
// Used to verify that the importer returns a sensible error (not a panic)
// when given a file that is not a .docx at all.
func docxNotAZip() []byte {
	// Deliberately invalid: begins with bytes that cannot be a PK header.
	return []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE,
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}
}

// docxHindi returns a .docx whose body contains a paragraph of Devanagari
// script text. Used to verify that the importer correctly handles multi-byte
// UTF-8 encodings and non-Latin scripts.
func docxHindi() []byte {
	const doc = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:r>
        <w:t>नमस्ते दुनिया। यह एक परीक्षण दस्तावेज़ है।</w:t>
      </w:r>
    </w:p>
  </w:body>
</w:document>`

	buf, zw := newDocx()
	addFile(zw, "word/document.xml", doc)
	return sealDocx(buf, zw, contentTypesXML, docRelsXML)
}

// ---------------------------------------------------------------------------
// Smoke test: every fixture that is a valid zip must contain word/document.xml
// ---------------------------------------------------------------------------

// TestDocxFixturesAreZips verifies that every fixture known to be a valid zip
// archive can be opened with archive/zip and contains a word/document.xml entry.
// The zip-bomb and not-a-zip fixtures are intentionally excluded.
func TestDocxFixturesAreZips(t *testing.T) {
	fixtures := map[string][]byte{
		"simple":         docxSimple(),
		"rich":           docxRich(),
		"externalImage":  docxExternalImage(),
		"javascriptLink": docxJavascriptLink(),
		"deepNesting":    docxDeepNesting(),
		"manyEntries":    docxManyEntries(),
		"macro":          docxMacro(),
		"hindi":          docxHindi(),
	}

	for name, data := range fixtures {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("zip.NewReader: %v", err)
			}
			found := false
			for _, f := range r.File {
				if f.Name == "word/document.xml" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("word/document.xml not found in fixture %q", name)
			}
		})
	}
}
