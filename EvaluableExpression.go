package govaluate

import (
	"errors"
	"fmt"
)

const isoDateFormat string = "2006-01-02T15:04:05.999999999Z0700"
const shortCircuitHolder int = -1

var DUMMY_PARAMETERS = MapParameters(map[string]interface{}{})

// EvaluableExpression represents a set of ExpressionTokens which, taken together,
// are an expression that can be evaluated down into a single value.

type EvaluableExpression struct {

	// Represents the query format used to output dates. Typically only used when creating SQL or Mongo queries from an expression.
	// Defaults to the complete ISO8601 format, including nanoseconds.

	QueryDateFormat string

	// Whether or not to safely check types when evaluating.
	// If true, this library will return error messages when invalid types are used.
	// If false, the library will panic when operators encounter types they can't use.

	// This is exclusively for users who need to squeeze every ounce of speed out of the library as they can,
	// and you should only set this to false if you know exactly what you're doing.

	ChecksTypes bool

	tokens           []ExpressionToken
	evaluationStages *evaluationStage
	inputExpression  string
}

// Parses a new EvaluableExpression from the given [expression] string.
// Returns an error if the given expression has invalid syntax.
func NewEvaluableExpression(expression string) (*EvaluableExpression, error) {

	functions := make(map[string]ExpressionFunction)
	return NewEvaluableExpressionWithFunctions(expression, functions)
}

// Similar to [NewEvaluableExpression], except that instead of a string, an already-tokenized expression is given.
// This is useful in cases where you may be generating an expression automatically, or using some other parser (e.g., to parse from a query language)
func NewEvaluableExpressionFromTokens(tokens []ExpressionToken) (*EvaluableExpression, error) {

	var ret *EvaluableExpression
	var err error

	ret = new(EvaluableExpression)
	ret.QueryDateFormat = isoDateFormat

	err = checkBalance(tokens)
	if err != nil {
		return nil, err
	}

	err = checkExpressionSyntax(tokens)
	if err != nil {
		return nil, err
	}

	ret.tokens, err = optimizeTokens(tokens)
	if err != nil {
		return nil, err
	}

	ret.evaluationStages, err = planStages(ret.tokens)
	if err != nil {
		return nil, err
	}

	ret.ChecksTypes = true
	return ret, nil
}

// Similar to [NewEvaluableExpression], except enables the use of user-defined functions.
// Functions passed into this will be available to the expression.
func NewEvaluableExpressionWithFunctions(expression string, functions map[string]ExpressionFunction) (*EvaluableExpression, error) {

	var ret *EvaluableExpression
	var err error

	ret = new(EvaluableExpression)
	ret.QueryDateFormat = isoDateFormat
	ret.inputExpression = expression

	ret.tokens, err = parseTokens(expression, functions)
	if err != nil {
		return nil, err
	}

	err = checkBalance(ret.tokens)
	if err != nil {
		return nil, err
	}

	err = checkExpressionSyntax(ret.tokens)
	if err != nil {
		return nil, err
	}

	ret.tokens, err = optimizeTokens(ret.tokens)
	if err != nil {
		return nil, err
	}

	ret.evaluationStages, err = planStages(ret.tokens)
	if err != nil {
		return nil, err
	}

	ret.ChecksTypes = true
	return ret, nil
}

// Same as `Eval`, but automatically wraps a map of parameters into a `govalute.Parameters` structure.

func (this EvaluableExpression) Evaluate(parameters map[string]interface{}) (interface{}, error) {

	if parameters == nil {
		return this.Eval(nil)
	}

	return this.Eval(MapParameters(parameters))
}

// Runs the entire expression using the given [parameters].
// e.g., If the expression contains a reference to the variable "foo", it will be taken from `parameters.Get("foo")`.

// This function returns errors if the combination of expression and parameters cannot be run,
// such as if a variable in the expression is not present in [parameters].

// In all non-error circumstances, this returns the single value result of the expression and parameters given.
// e.g., if the expression is "1 + 1", this will return 2.0.
// e.g., if the expression is "foo + 1" and parameters contains "foo" = 2, this will return 3.0

func (this EvaluableExpression) Eval(parameters Parameters) (interface{}, error) {

	if this.evaluationStages == nil {
		return nil, nil
	}

	if parameters != nil {
		parameters = &sanitizedParameters{parameters}
	} else {
		parameters = DUMMY_PARAMETERS
	}

	return this.evaluateStage(this.evaluationStages, parameters)
}

func (this EvaluableExpression) evaluateStage(stage *evaluationStage, parameters Parameters) (interface{}, error) {

	var left, right interface{}
	var err error

	if stage.leftStage != nil {
		left, err = this.evaluateStage(stage.leftStage, parameters)
		if err != nil {
			return nil, err
		}
	}

	if stage.isShortCircuitable() {
		switch stage.symbol {
		case AND:
			if left == false {
				return false, nil
			}
		case OR:
			if left == true {
				return true, nil
			}
		case COALESCE:
			if left != nil {
				return left, nil
			}

		case TERNARY_TRUE:
			if left == false {
				right = shortCircuitHolder
			}
		case TERNARY_FALSE:
			if left != nil {
				right = shortCircuitHolder
			}
		}
	}

	if right != shortCircuitHolder && stage.rightStage != nil {
		right, err = this.evaluateStage(stage.rightStage, parameters)
		if err != nil {
			return nil, err
		}
	}

	if this.ChecksTypes {
		if stage.typeCheck == nil {

			err = typeCheck(stage.leftTypeCheck, left, stage.symbol, stage.typeErrorFormat)
			if err != nil {
				return nil, err
			}

			err = typeCheck(stage.rightTypeCheck, right, stage.symbol, stage.typeErrorFormat)
			if err != nil {
				return nil, err
			}
		} else {
			// special case where the type check needs to know both sides to determine if the operator can handle it
			if !stage.typeCheck(left, right) {
				errorMsg := fmt.Sprintf(stage.typeErrorFormat, left, stage.symbol.String())
				return nil, errors.New(errorMsg)
			}
		}
	}

	return stage.operator(left, right, parameters)
}

func isSubset(subset, superset []string) bool {

	for _, sub := range subset {
		found := false
		for _, super := range superset {
			if sub == super {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func (this EvaluableExpression) CanJoin(parameters map[string]interface{}) (bool, []string, error) {

	if parameters == nil {
		return this.canJoinInternal(nil)
	}

	return this.canJoinInternal(MapParameters(parameters))
}

func (this EvaluableExpression) canJoinInternal(parameters Parameters) (bool, []string, error) {

	if this.evaluationStages == nil {
		return true, nil, nil
	}

	if parameters != nil {
		parameters = &sanitizedParameters{parameters}
	} else {
		parameters = DUMMY_PARAMETERS
	}

	return this.canJoin(this.evaluationStages, parameters)
}

func (this EvaluableExpression) canJoin(stage *evaluationStage, parameters Parameters) (bool, []string, error) {

	var left, right bool
	var leftKeys, rightKeys []string
	var err error

	if stage.symbol == VALUE {
		v, ok := stage.token.Value.(string)
		if !ok {
			return false, nil, errors.New("value is not a string")
		}
		joinKeys, err := parameters.Get(v)
		if err != nil {
			return false, nil, err
		}

		if joinKeys == nil {
			return false, nil, fmt.Errorf("no group keys for %v", v)
		}

		if keys, ok := joinKeys.([]string); ok {
			return true, keys, nil
		}

		return false, nil, fmt.Errorf("Group keys for %v are not a string array", v)
	}

	if stage.leftStage != nil {
		left, leftKeys, err = this.canJoin(stage.leftStage, parameters)
		if err != nil {
			return false, nil, err
		}
	}

	if stage.rightStage != nil {
		right, rightKeys, err = this.canJoin(stage.rightStage, parameters)
		if err != nil {
			return false, nil, err
		}
	}

	if left && right {
		if isSubset(leftKeys, rightKeys) {
			return true, rightKeys, nil
		}

		if isSubset(rightKeys, leftKeys) {
			return true, leftKeys, nil
		}
	}

	if stage.leftStage == nil && right {
		return true, rightKeys, nil
	}

	if stage.rightStage == nil && left {
		return true, leftKeys, nil
	}

	if stage.leftStage == nil && stage.rightStage == nil {
		return true, []string{}, nil
	}

	return false, nil, fmt.Errorf("Group keys must match or be a subset of the other but found left: %v, right: %v", leftKeys, rightKeys)
}

func typeCheck(check stageTypeCheck, value interface{}, symbol OperatorSymbol, format string) error {

	if check == nil {
		return nil
	}

	if check(value) {
		return nil
	}

	errorMsg := fmt.Sprintf(format, value, symbol.String())
	return errors.New(errorMsg)
}

// Returns an array representing the ExpressionTokens that make up this expression.

func (this EvaluableExpression) Tokens() []ExpressionToken {

	return this.tokens
}

// Returns a string representation of this expression.

func (this EvaluableExpression) String() string {
	if this.inputExpression != "" {
		return this.inputExpression
	}

	var expressionText string
	for _, val := range this.Tokens() {
		switch val.Kind {
		case VARIABLE:
			expressionText += fmt.Sprintf("[%+v]", val.Meta)
		case STRING, TIME:
			expressionText += fmt.Sprintf("'%+v'", val.Meta)
		case COMPARATOR, LOGICALOP, MODIFIER, TERNARY:
			expressionText += fmt.Sprintf(" %+v ", val.Meta)
		case SEPARATOR:
			expressionText += fmt.Sprintf("%+v ", val.Meta)
		default:
			expressionText += fmt.Sprintf("%+v", val.Meta)
		}
	}

	return expressionText
}

// Returns a string representation of this expression without brackets for vars.

func (this EvaluableExpression) ExpressionString() string {
	if this.inputExpression != "" {
		return this.inputExpression
	}

	var expressionText string
	for _, val := range this.Tokens() {
		switch val.Kind {
		case VARIABLE:
			expressionText += fmt.Sprintf("%+v", val.Meta)
		case STRING, TIME:
			expressionText += fmt.Sprintf("'%+v'", val.Meta)
		case COMPARATOR, LOGICALOP, MODIFIER, TERNARY:
			expressionText += fmt.Sprintf(" %+v ", val.Meta)
		case SEPARATOR:
			expressionText += fmt.Sprintf("%+v ", val.Meta)
		default:
			expressionText += fmt.Sprintf("%+v", val.Meta)
		}
	}

	return expressionText
}

// Returns an array representing the variables contained in this EvaluableExpression.

func (this EvaluableExpression) Vars() []string {
	var varlist []string
	for _, val := range this.Tokens() {
		if val.Kind == VARIABLE {
			varlist = append(varlist, val.Value.(string))
		}
	}
	return varlist
}
