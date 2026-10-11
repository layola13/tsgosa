class Box { n: i32 = 0; }
class C { n: i32 = 0; constructor(q: Box) { this.n = q.n; } }
function main(): i32 {
  const b = new Box();
  b.n = 41;
  const c = new C(b);
  console.log(c.n);
  return 0;
}
