package teams

import "time"

// Role определяет роль пользователя в команде
type Role string

const (
	// RoleOwner даёт полный доступ создателю команды
	RoleOwner Role = "owner"
	// RoleAdmin даёт административный доступ к команде
	RoleAdmin Role = "admin"
	// RoleMember даёт доступ обычного участника команды
	RoleMember Role = "member"
)

// Team представляет команду пользователей
type Team struct {
	ID        uint64
	Name      string
	CreatedBy uint64
	CreatedAt time.Time
}

// CreateInput содержит данные для создания команды
type CreateInput struct {
	Name string
}

// InviteInput содержит данные для добавления пользователя в команду
type InviteInput struct {
	Email string
	Role  Role
}

// ChangeMemberRoleInput содержит новую роль существующего участника
type ChangeMemberRoleInput struct {
	Role Role
}
