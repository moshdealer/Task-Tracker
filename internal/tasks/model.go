package tasks

import (
	"encoding/json"
	"time"
)

// Status определяет состояние задачи
type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusHold       Status = "hold"
	StatusDone       Status = "done"
	StatusCancel     Status = "cancel"
)

// Task представляет задачу команды
type Task struct {
	ID          uint64     `json:"id"`
	TeamID      uint64     `json:"team_id"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	Status      Status     `json:"status"`
	CreatedBy   uint64     `json:"created_by"`
	AssigneeID  *uint64    `json:"assignee_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Version     uint64     `json:"version"`
}

// CreateInput содержит данные для создания задачи
type CreateInput struct {
	TeamID      uint64
	Title       string
	Description *string
	Status      Status
	AssigneeID  *uint64
}

// UpdateInput содержит частичное изменение задачи
type UpdateInput struct {
	Version        uint64
	TitleSet       bool
	Title          string
	DescriptionSet bool
	Description    *string
	StatusSet      bool
	Status         Status
	AssigneeIDSet  bool
	AssigneeID     *uint64
}

// ListFilter содержит фильтры и пагинацию списка задач
type ListFilter struct {
	TeamID     uint64
	Status     *Status
	AssigneeID *uint64
	Limit      int
	Offset     int
}

// HistoryEntry представляет запись истории задачи
type HistoryEntry struct {
	ID        uint64          `json:"id"`
	TaskID    uint64          `json:"task_id"`
	ChangedBy uint64          `json:"changed_by"`
	Changes   json.RawMessage `json:"changes"`
	CreatedAt time.Time       `json:"created_at"`
}

type fieldChange struct {
	Old any `json:"old"`
	New any `json:"new"`
}

type taskChanges map[string]fieldChange

func validStatus(status Status) bool {
	switch status {
	case StatusTodo, StatusInProgress, StatusHold, StatusDone, StatusCancel:
		return true
	default:
		return false
	}
}

func terminalStatus(status Status) bool {
	return status == StatusDone || status == StatusCancel
}
