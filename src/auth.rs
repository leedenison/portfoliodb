use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};
use tokio::sync::Mutex;
use tonic::Status;
use http::Request;
use tonic::body::Body;
use http::{Response, StatusCode, HeaderValue};
use tower::Service;
use tower::Layer;
use std::collections::HashMap;

use crate::db::DatabaseManager;

#[derive(Clone)]
pub struct AuthMiddleware<S> {
    inner: S,
    database_url: String,
    db: Arc<Mutex<Option<DatabaseManager>>>,
}

impl<S> AuthMiddleware<S> {
    pub fn new(inner: S, database_url: String) -> Self {
        Self {
            inner,
            database_url,
            db: Arc::new(Mutex::new(None)),
        }
    }
}

impl<S> Service<Request<Body>> for AuthMiddleware<S>
where
    S: Service<Request<Body>, Response = Response<Body>> + Clone + Send + 'static,
    S::Future: Send + 'static,
{
    type Response = Response<Body>;
    type Error = S::Error;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx)
    }

    fn call(&mut self, mut req: Request<Body>) -> Self::Future {
        let mut inner = self.inner.clone();
        let db_url = self.database_url.clone();
        let db_handle = self.db.clone();

        Box::pin(async move {
            let email: Option<String> = req.headers()
                .get("x-auth-request-email")
                .and_then(|val| val.to_str().ok())
                .map(|s| s.to_owned());

            let email = match email {
                Some(email) => email,
                None => return Ok(grpc_error_response(Status::unauthenticated("Missing or invalid email header"))),
            };

            let db_manager = match get_db_manager(&db_handle, &db_url).await {
                Ok(db) => db,
                Err(status) => return Ok(grpc_error_response(status)),
            };

            match db_manager.get_user_id_by_email(&email).await {
                Ok(Some(user_id)) => {
                    let extensions = req.extensions_mut();
                    let map = extensions.get_mut::<HashMap<String, i64>>();
                    if let Some(map) = map {
                        map.insert("user_id".to_string(), user_id);
                    } else {
                        let mut map = HashMap::new();
                        map.insert("user_id".to_string(), user_id);
                        extensions.insert(map);
                    }
                }
                Ok(None) => {
                    return Ok(grpc_error_response(Status::permission_denied(
                        "No user found for provided email",
                    )))
                }
                Err(e) => {
                    return Ok(grpc_error_response(Status::internal(format!("DB error: {}", e))))
                }
            }

            inner.call(req).await
        })
    }
}

// Optional helper for easier service composition
#[derive(Clone)]
pub struct AuthLayer {
    database_url: String,
}

impl AuthLayer {
    pub fn new(database_url: String) -> Self {
        Self { database_url }
    }
}

impl<S> Layer<S> for AuthLayer {
    type Service = AuthMiddleware<S>;

    fn layer(&self, inner: S) -> Self::Service {
        AuthMiddleware::new(inner, self.database_url.clone())
    }
}

async fn get_db_manager(
    db_handle: &Arc<Mutex<Option<DatabaseManager>>>,
    database_url: &str,
) -> Result<DatabaseManager, Status> {
    let mut db_guard = db_handle.lock().await;
    if db_guard.is_none() {
        let db_result = DatabaseManager::new(database_url).await;
        match db_result {
            Ok(db) => *db_guard = Some(db),
            Err(e) => return Err(Status::internal(format!("DB init failed: {}", e))),
        }
    }
    Ok(db_guard.as_ref().unwrap().clone())
}

fn grpc_error_response(status: Status) -> Response<Body> {
    let mut response = Response::new(Body::empty());

    *response.status_mut() = StatusCode::OK;
    response.headers_mut().insert(
        http::header::CONTENT_TYPE,
        HeaderValue::from_static("application/grpc"),
    );
    
    let status_code_str = (status.code() as u32).to_string();
    response.headers_mut().insert(
        "grpc-status",
        HeaderValue::from_str(&status_code_str).unwrap_or_else(|_| HeaderValue::from_static("0")),
    );
    response.headers_mut().insert(
        "grpc-message",
        HeaderValue::from_str(status.message()).unwrap_or_else(|_| HeaderValue::from_static("invalid")),
    );

    response
}