#[macro_export]
macro_rules! automock {
    // Require a body; this is the default you want in non-test builds.
    (fn $name:ident $sig:tt $body:block) => {
        // Tests (or when feature "automock" is on): replace with a panic body.
        #[cfg(any(test, feature = "automock"))]
        fn $name $sig {
            panic!(concat!("unexpected call to ", stringify!($name)));
        }

        // Normal builds: use the supplied default implementation.
        #[cfg(not(any(test, feature = "automock")))]
        fn $name $sig $body
    };
}
