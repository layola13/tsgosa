class Box { r: f64 = 0; }
class C { r: f64 = 0; constructor(q: Box) { this.r = q.r + 0.5; } }
function main(): i32 {
  const b = new Box();
  b.r = 1.0;
  const c = new C(b);
  console.log(c.r);
  return 0;
}
