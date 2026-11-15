class Base { hello() {} }
class Child extends Base {
  greet() { this.hello(); this.wave(); }
  wave() {}
}
function helper() {}
function run() { const c = new Child(); c.greet(); helper(); }
