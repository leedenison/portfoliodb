#[macro_export]
macro_rules! automock {
    // with body
    //
    // e.g.:
    //   automock!(async fn foo<T>(x: T) -> usize where T: Copy { 0 });
    //
    ($(#[$meta:meta])* $($async:ident)? fn $name:ident
        $(<$($gen:tt)*>)?
        ($($args:tt)*)
        -> $ret:ty
        $(where $($where:tt)*)?
        $body:block
    ) => {
        #[cfg(any(test, feature = "automock"))]
        $(#[$meta])*
        $($async)? fn $name $(<$($gen)*>)? ($($args)*) -> $ret $(where $($where)*)? {
            panic!(concat!("unexpected call to ", stringify!($name)));
        }

        #[cfg(not(any(test, feature = "automock")))]
        $(#[$meta])*
        $($async)? fn $name $(<$($gen)*>)? ($($args)*) -> $ret $(where $($where)*)? $body
    };

    // without body
    //
    // e.g.:
    //   automock!(fn bar(&self, x: i32) -> Result<()>;);
    //
    ($(#[$meta:meta])* $($async:ident)? fn $name:ident
        $(<$($gen:tt)*>)?
        ($($args:tt)*)
        -> $ret:ty
        $(where $($where:tt)*)?
        ;
    ) => {
        #[cfg(any(test, feature = "automock"))]
        $(#[$meta])*
        $($async)? fn $name $(<$($gen)*>)? ($($args)*) -> $ret $(where $($where)*)? {
            panic!(concat!("unexpected call to ", stringify!($name)));
        }

        #[cfg(not(any(test, feature = "automock")))]
        $(#[$meta])*
        $($async)? fn $name $(<$($gen)*>)? ($($args)*) -> $ret $(where $($where)*)? ;
    };
}
