class Form {
  save() { this.validate(); }
  validate() {}
}
function helper() {}
function run() { const f = new Form(); f.save(); helper(); }
interface Shape { area(): number }
class Sq implements Shape { area() { return 4; } }
export function total(s: Shape, cb: () => void) { cb(); console.log(s.area()); return [1].map(x => x); }
