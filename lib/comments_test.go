package lib

import "testing"

func TestJiraCommentRegex(t *testing.T) {
	var fields = jCommentRegex.FindStringSubmatch(`Comment [(ID 484163403)|https://github.com] from GitHub user [bilbo-baggins|https://github.com/bilbo-baggins] (Bilbo Baggins) at 16:27 PM, April 17 2019:

Bla blibidy bloo bla`)

	if len(fields) != 6 {
		t.Fatalf("Regex failed to parse fields %v", fields)
	}

	if fields[1] != "484163403" {
		t.Fatalf("Expected field[1] = 484163403; Got field[1] = %s", fields[1])
	}

	if fields[2] != "bilbo-baggins" {
		t.Fatalf("Expected field[2] = bilbo-baggins; Got field[2] = %s", fields[2])
	}

	if fields[3] != "Bilbo Baggins" {
		t.Fatalf("Expected field[3] = Bilbo Baggins; Got field[3] = %s", fields[3])
	}

	if fields[4] != "16:27 PM, April 17 2019" {
		t.Fatalf("Expected field[4] = 16:27 PM, April 17 2019; Got field[4] = %s", fields[4])
	}

	if fields[5] != "Bla blibidy bloo bla" {
		t.Fatalf("Expected field[5] = Bla blibidy bloo bla; Got field[5] = %s", fields[5])
	}
}
