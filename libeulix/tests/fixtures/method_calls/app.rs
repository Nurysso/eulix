use crate::base::Base;
use crate::helpers as api;
use external_missing as ext;
fn send() -> i32 { 0 }
fn inherited() -> i32 { 0 }
trait Client: Base {
    fn send(&self) -> i32 { 1 }
    fn run(&mut self) { self.send(); self.inherited(); }
    fn factory(&self) { Self::send(self); }
    fn bare(&self) { send(); }
    fn static_call() { send(); }
    fn missing(&self) { self.only_elsewhere(); }
    fn shadow(&self, send: fn()) { self.send(); }
}
struct Other;
impl Other {
    fn send(&self) -> i32 { 2 }
    fn unique(&self) -> i32 { 3 }
    fn only_elsewhere(&self) -> i32 { 4 }
}
fn unknown(obj: Other) { obj.send(); obj.unique(); }
fn imported() { api::helper(); ext::unique(); }
fn helper() -> i32 { 9 }
