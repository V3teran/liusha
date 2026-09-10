package planner

// GlobalAssessment 是全局评估结果
type GlobalAssessment struct {
	Status          string
	Progress        string
	Strategy        string
	Reasoning       string
	NewActions      []NewAction
	ActionsToSteer  []ActionSteer
	ActionsToKill   []string
}

// NewAction 是新建的 Action
type NewAction struct {
	Instruction string
	Goal        string
	Complexity  string
	Target      interface{}
	Priority    string // critical/high/medium/low
	DependsOn   []string
}

// ActionSteer 是需要调整的 Action
type ActionSteer struct {
	ActionID  string
	Directive string
}
