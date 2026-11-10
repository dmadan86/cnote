use serde::Serialize;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ErrorCode {
    Unauthenticated,
    Forbidden,
    NotFound,
    Invalid,
    Conflict,
    RateLimited,
    PlanLimit,
    Internal,
}

impl ErrorCode {
    pub fn http_status(self) -> u16 {
        match self {
            Self::Unauthenticated => 401,
            Self::Forbidden => 403,
            Self::NotFound => 404,
            Self::Invalid => 400,
            Self::Conflict => 409,
            Self::RateLimited => 429,
            Self::PlanLimit => 402,
            Self::Internal => 500,
        }
    }
}

#[derive(Debug, thiserror::Error, Serialize)]
#[error("{code:?}: {message}")]
pub struct CmdError {
    pub code: ErrorCode,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub details: Option<serde_json::Value>,
}

pub type CmdResult<T> = Result<T, CmdError>;

impl CmdError {
    pub fn new(code: ErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            details: None,
        }
    }
    pub fn not_found(what: &str) -> Self {
        Self::new(ErrorCode::NotFound, format!("{what} not found"))
    }
    pub fn forbidden(msg: impl Into<String>) -> Self {
        Self::new(ErrorCode::Forbidden, msg)
    }
    pub fn invalid(msg: impl Into<String>) -> Self {
        Self::new(ErrorCode::Invalid, msg)
    }
    pub fn conflict(msg: impl Into<String>) -> Self {
        Self::new(ErrorCode::Conflict, msg)
    }
    pub fn internal(msg: impl Into<String>) -> Self {
        Self::new(ErrorCode::Internal, msg)
    }
}

impl From<sqlx::Error> for CmdError {
    fn from(e: sqlx::Error) -> Self {
        if let sqlx::Error::RowNotFound = e {
            return Self::new(ErrorCode::NotFound, "not found");
        }
        if let Some(db) = e.as_database_error() {
            match db.code().as_deref() {
                Some("23505") => return Self::conflict(db.message().to_string()),
                Some("23503") | Some("23514") | Some("22P02") | Some("P0001") => {
                    return Self::invalid(db.message().to_string());
                }
                Some("42501") => return Self::forbidden("row-level security denied the operation"),
                _ => {}
            }
        }
        tracing::error!(error = %e, "database error");
        Self::internal("database error")
    }
}

impl From<serde_json::Error> for CmdError {
    fn from(e: serde_json::Error) -> Self {
        Self::invalid(e.to_string())
    }
}

impl From<anyhow::Error> for CmdError {
    fn from(e: anyhow::Error) -> Self {
        tracing::error!(error = %e, "internal error");
        Self::internal(e.to_string())
    }
}
