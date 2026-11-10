use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Role {
    Guest,
    Member,
    Admin,
    Owner,
}

impl Role {
    pub fn parse(s: &str) -> Self {
        match s {
            "owner" => Self::Owner,
            "admin" => Self::Admin,
            "member" => Self::Member,
            _ => Self::Guest,
        }
    }
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Owner => "owner",
            Self::Admin => "admin",
            Self::Member => "member",
            Self::Guest => "guest",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PrincipalKind {
    User,
    Agent,
    ApiKey,
    System,
}

/// Who is calling a command. Agents act on behalf of a human (`user_id`) and never exceed that human's role.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Principal {
    pub kind: PrincipalKind,
    pub user_id: Option<Uuid>,
    pub agent_id: Option<Uuid>,
    pub workspace_id: Uuid,
    pub role: Role,
    /// None = every group the role allows.
    pub scopes: Option<Vec<String>>,
}

impl Principal {
    pub fn user(user_id: Uuid, workspace_id: Uuid, role: Role) -> Self {
        Self {
            kind: PrincipalKind::User,
            user_id: Some(user_id),
            agent_id: None,
            workspace_id,
            role,
            scopes: None,
        }
    }
    pub fn system(workspace_id: Uuid) -> Self {
        Self {
            kind: PrincipalKind::System,
            user_id: None,
            agent_id: None,
            workspace_id,
            role: Role::Owner,
            scopes: None,
        }
    }
    pub fn is_machine(&self) -> bool {
        matches!(self.kind, PrincipalKind::Agent | PrincipalKind::ApiKey)
    }
    /// (actor_type, actor_id, on_behalf_of) for the event log.
    pub fn actor(&self) -> (&'static str, Option<String>, Option<Uuid>) {
        match self.kind {
            PrincipalKind::Agent => ("agent", self.agent_id.map(|a| a.to_string()), self.user_id),
            PrincipalKind::System => ("system", None, self.user_id),
            _ => ("user", self.user_id.map(|u| u.to_string()), None),
        }
    }
}
