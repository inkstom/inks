package search

import "testing"

const hcl = `resource "aws_s3_bucket" "logs" {
  bucket = "my-logs"
  tags = {
    Name = "logs { not a brace }"
  }
}

resource "aws_instance" "web" {
  ami           = "ami-123"
  instance_type = "t3.micro"
}

resource "aws_s3_bucket" "data" {
  bucket = "my-data"
}
`

func TestLiteralAndWholeWord(t *testing.T) {
	if n := len(New("bucket", Options{WholeWord: true}).FindAll(hcl, 0, 0)); n != 2 {
		t.Fatalf("whole word: %d", n)
	}
	if n := len(New("BUCKET", Options{}).FindAll(hcl, 0, 0)); n != 4 {
		t.Fatalf("case-insensitive: %d", n)
	}
	if n := len(New("{\n  bucket", Options{}).FindAll(hcl, 0, 0)); n != 2 {
		t.Fatalf("multi-line literal: %d", n)
	}
}

func TestRegexReplacement(t *testing.T) {
	m := New(`resource "(\w+)" "(\w+)"`, Options{Regex: true})
	r := m.FindAll(hcl, 0, 0)
	if len(r) != 3 {
		t.Fatalf("regex matches: %d", len(r))
	}
	if got := m.Replacement(r[1], `module "\U$2\E_$1"`, false); got != `module "WEB_aws_instance"` {
		t.Fatalf("replacement: %q", got)
	}
	if got := ExpandRegex(`\u$1-${2}`, Match{Text: "x", Groups: []string{"x", "foo", "bar"}}); got != "Foo-bar" {
		t.Fatalf("expand: %q", got)
	}
}

func TestStructural(t *testing.T) {
	m := New(`resource "aws_s3_bucket" :[name] {:[body]}`, Options{Structural: true})
	if !m.Valid() {
		t.Fatal(m.Err)
	}
	blocks := m.FindAll(hcl, 0, 0)
	if len(blocks) != 2 {
		t.Fatalf("blocks: %d", len(blocks))
	}
	if blocks[0].Holes["name"].Text != `"logs"` {
		t.Fatalf("hole: %q", blocks[0].Holes["name"].Text)
	}
	if got := m.Replacement(blocks[1], "module :[name] {:[body]}", false); got != "module \"data\" {\n  bucket = \"my-data\"\n}" {
		t.Fatalf("structural replace: %q", got)
	}
	v := New(`instance_type = :[v\n]`, Options{Structural: true}).FindAll(hcl, 0, 0)
	if len(v) != 1 || v[0].Holes["v"].Text != `"t3.micro"` {
		t.Fatalf("line hole: %+v", v)
	}
	two := New(`resource :[t] :[n] {:[body]}`, Options{Structural: true}).FindAll(hcl, 0, 0)
	if len(two) != 3 || two[1].Holes["t"].Text != `"aws_instance"` || two[1].Holes["n"].Text != `"web"` {
		t.Fatalf("adjacent holes: %+v", two[1].Holes)
	}
	calls := New("foo(:[a])", Options{Structural: true}).FindAll(`x = foo(1, g(2, [3]), "a)") + foo()`, 0, 0)
	if len(calls) != 2 || calls[0].Text != `foo(1, g(2, [3]), "a)")` || calls[1].Text != "foo()" {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestCaseAndFilters(t *testing.T) {
	if ApplyCase("Foo", "bar") != "Bar" || ApplyCase("FOO", "bar") != "BAR" || ApplyCase("foo", "Bar") != "bar" {
		t.Fatal("preserve case")
	}
	f := PathFilter("*.tf, modules/**", "node_modules, *.lock.hcl")
	if !f("main.tf") || !f("modules/vpc/variables.txt") || f("README.md") || f("node_modules/x/main.tf") {
		t.Fatal("path filter")
	}
}

func TestUnicodeOffsets(t *testing.T) {
	text := "héllo wörld héllo"
	r := New("héllo", Options{}).FindAll(text, 0, 0)
	if len(r) != 2 || text[r[1].From:r[1].To] != "héllo" {
		t.Fatalf("unicode offsets: %+v", r)
	}
}
