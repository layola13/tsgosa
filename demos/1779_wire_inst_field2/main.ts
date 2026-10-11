class Box { n: i32 = 0; m: i32 = 0; }
class C { n: i32 = 0; constructor(q: Box) { this.n = q.m; } }
function main(): i32 {
  const b = new Box();
  b.n = 1;
  b.m = 9;
  const c = new C(b);
  console.log(c.n);
  return 0;
}
