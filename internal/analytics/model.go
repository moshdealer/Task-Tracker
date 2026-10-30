package analytics

// Stats содержит обязательные метрики команды
type Stats struct {
	TeamID                 uint64        `json:"team_id"`
	TasksByStatus          StatusCounts  `json:"tasks_by_status"`
	TopAssigneesLast30Days []TopAssignee `json:"top_assignees_last_30_days"`
	AverageCloseHours      *float64      `json:"average_close_hours"`
	CommentsCount          uint64        `json:"comments_count"`
}

// StatusCounts содержит количество задач по каждому допустимому статусу
type StatusCounts struct {
	Todo       uint64 `json:"todo"`
	InProgress uint64 `json:"in_progress"`
	Hold       uint64 `json:"hold"`
	Done       uint64 `json:"done"`
	Cancel     uint64 `json:"cancel"`
}

// TopAssignee представляет исполнителя в рейтинге закрытых задач
type TopAssignee struct {
	AssigneeID  uint64 `json:"assignee_id"`
	Name        string `json:"name"`
	ClosedTasks uint64 `json:"closed_tasks"`
}
