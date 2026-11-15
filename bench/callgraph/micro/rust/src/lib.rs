pub struct S;
impl S {
    pub fn new() -> S { S }
    pub fn get(&self) -> i32 { self.inner() }
    fn inner(&self) -> i32 { 1 }
}
fn helper() {}
pub fn run() { let s = S::new(); s.get(); helper(); }
pub fn ext() -> usize { let mut v: Vec<u8> = Vec::new(); v.push(1); let s = S::new(); let o = Some(3); helper(); s
    .get(); v.len() + o.unwrap() }
