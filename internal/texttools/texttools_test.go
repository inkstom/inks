package texttools

import (
	"strings"
	"testing"
)

const tf = `# web server
resource "aws_instance" "web" {
  instance_type = "t3.micro"
  ami = "ami-123"
  tags = {
    Name = "web }"
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "data"
}

variable "region" {
  default = "us-east-1"
}
`

func TestSortBlocks(t *testing.T) {
	out, err := SortBlocks(tf, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# web server\nresource \"aws_instance\"") {
		t.Fatalf("comment should stay attached:\n%s", out)
	}
	if strings.Index(out, "aws_instance") > strings.Index(out, "aws_s3_bucket") {
		t.Fatal("order")
	}
	if !strings.Contains(out, "Name = \"web }\"\n  }\n}") {
		t.Fatal("brace inside string ended the block")
	}
	byName, _ := SortBlocks(tf, `^\w+\s+"[^"]*"\s+"([^"]*)"`, false)
	if strings.Index(byName, `"data"`) > strings.Index(byName, `"web"`) {
		t.Fatal("sort by key")
	}
}

func TestBlockBodyAndAlign(t *testing.T) {
	body := "  instance_type = \"t3.micro\"\n  ami = \"ami-123\"\n  tags = {\n    Name = \"web\"\n  }\n"
	want := "  ami = \"ami-123\"\n  instance_type = \"t3.micro\"\n  tags = {\n    Name = \"web\"\n  }\n"
	if got := SortBlockBody(body); got != want {
		t.Fatalf("block body:\n%q", got)
	}
	s, e, ok := EnclosingBlock(tf, strings.Index(tf, "ami"))
	if !ok || !strings.HasPrefix(tf[s:e], "  instance_type") {
		t.Fatalf("enclosing block: %q", tf[s:e])
	}
	if got := AlignOn("  a = 1\n  long_name = 2\n  b == c", "="); got != "  a         = 1\n  long_name = 2\n  b == c" {
		t.Fatalf("align: %q", got)
	}
}

func TestLineTools(t *testing.T) {
	cases := []struct{ got, want string }{
		{SortLines("b10\nb9\na\n", "natural", false, 0, ""), "a\nb9\nb10\n"},
		{SortLines("3\n10\n2", "numeric", false, 0, ""), "2\n3\n10"},
		{SortLines("x 3\ny 1\nz 2", "numeric", false, 2, ""), "y 1\nz 2\nx 3"},
		{Uniq("a\nb\na\n", false, false, false, false), "a\nb\n"},
		{Uniq("a\na\nb\na", true, false, false, false), "a\nb\na"},
		{strings.TrimSpace(strings.Split(Uniq("a\nb\na", false, true, false, false), "\n")[0]), "2 a"},
		{Cut("a,b,c\n1,2,3", "-d, -f1,3"), "a,c\n1,3"},
		{Cut("a b  c", "2-"), "b c"},
		{Translate("hello", "a-z A-Z"), "HELLO"},
		{Translate("hello", "-d l"), "heo"},
		{JoinWith("a\nb\nc", ", "), "a, b, c"},
		{SplitOn("a, b,c", ","), "a\nb\nc"},
		{Zip([]string{"a\nb", "1\n2"}, "="), "a=1\nb=2"},
		{JoinOnField("k1 a\nk2 b", "k2 x\nk1 y", 1, ""), "k1 a y\nk2 b x"},
		{SqueezeBlankLines("a\n\n\n\nb"), "a\n\nb"},
		{RemoveBlankLines("a\n\n b\n"), "a\n b\n"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q want %q", i, c.got, c.want)
		}
	}
	if out, _ := Grep("foo\nbar\nfoo2", "foo", false, true); out != "foo\nfoo2" {
		t.Errorf("grep: %q", out)
	}
	if out, _ := Grep("foo\nbar", "foo", true, true); out != "bar" {
		t.Errorf("grep -v: %q", out)
	}
	if out, _ := Sed("foo bar foo", "s/foo/baz/g"); out != "baz bar baz" {
		t.Errorf("sed g: %q", out)
	}
	if out, _ := Sed("a-b", `s/\(a\)-\(b\)/\2-\1/`); out != "b-a" {
		t.Errorf("sed groups: %q", out)
	}
	if out, _ := Sed("foo foo", "s/foo/x/"); out != "x foo" {
		t.Errorf("sed first only: %q", out)
	}
}
