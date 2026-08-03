// Package playbook 实现可复用猎手组合（playbook + playbook_hunter 表）的持久化层。
// 一个 playbook 是一组带顺序的领域猎手；scenario 引用一个 playbook。
// import 时用别名 cfgplaybook。
package playbook

import "time"

// Playbook 是 playbook 表行的 Go 表示。
type Playbook struct {
	ID          string
	Code        string
	Name        string
	Description string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PlaybookHunter 是 playbook_hunter 组合关系行。
//   - Position：solo 时=body 拼接序；swarm 时=展示默认序
type PlaybookHunter struct {
	PlaybookID string
	HunterID   string
	Position   int
}

// NewParams 是 Store.Create / Store.Update 的入参。
type NewParams struct {
	Code        string
	Name        string
	Description string
	Enabled     bool
}
