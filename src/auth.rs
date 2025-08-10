use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};
use tonic::Status;
use http::Request;
use tonic::body::Body;
use http::{Response, StatusCode, HeaderValue};
use tower::Service;
use tower::Layer;
use std::collections::HashMap;

use crate::db::api::DataStore;

#[derive(Clone)]
pub struct AuthMiddleware<S> {
    inner: S,
    db: Arc<dyn DataStore + Send + Sync>,
}

impl<S> AuthMiddleware<S> {
    pub fn new(inner: S, db: Arc<dyn DataStore + Send + Sync>) -> Self {
        Self {
            inner,
            db,
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
        let db = self.db.clone();

        Box::pin(async move {
            let email: Option<String> = req.headers()
                .get("x-auth-request-email")
                .and_then(|val| val.to_str().ok())
                .map(|s| s.to_owned());

            let email = match email {
                Some(email) => email,
                None => return Ok(grpc_error_response(Status::unauthenticated("Missing or invalid email header"))),
            };

            match db.get_user_id_by_email(&email).await {
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
    db: Arc<dyn DataStore + Send + Sync>,
}

impl AuthLayer {
    pub fn new(db: Arc<dyn DataStore + Send + Sync>) -> Self {
        Self { db }
    }
}

impl<S> Layer<S> for AuthLayer {
    type Service = AuthMiddleware<S>;

    fn layer(&self, inner: S) -> Self::Service {
        AuthMiddleware::new(inner, self.db.clone())
    }
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