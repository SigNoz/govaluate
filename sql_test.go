package govaluate

import (
	"testing"
)

// Represents a test of correctly creating a SQL query string from an expression.

type QueryTest struct {
	Name     string
	Input    string
	Expected string
}

func runQueryTests(testCases []QueryTest, test *testing.T) {

	var expression *EvaluableExpression
	var actualQuery string
	var err error

	test.Logf("Running %d SQL translation test cases", len(testCases))

	// Run the test cases.
	for _, testCase := range testCases {

		expression, err = NewEvaluableExpression(testCase.Input)

		if err != nil {

			test.Logf("Test '%s' failed to parse: %s", testCase.Name, err)
			test.Logf("Expression: '%s'", testCase.Input)
			test.Fail()
			continue
		}

		actualQuery, err = expression.ToSQLQuery()

		if err != nil {

			test.Logf("Test '%s' failed to create query: %s", testCase.Name, err)
			test.Logf("Expression: '%s'", testCase.Input)
			test.Fail()
			continue
		}

		if actualQuery != testCase.Expected {

			test.Logf("Test '%s' did not create expected query.", testCase.Name)
			test.Logf("Actual: '%s', expected '%s'", actualQuery, testCase.Expected)
			test.Fail()
			continue
		}
	}
}
