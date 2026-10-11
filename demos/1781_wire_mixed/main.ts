class Box { n: i32 = 0; }
class C { n: i32 = 0; m: i32 = 0; constructor(q: Box, a: i32[]) { this.n = a.length; this.m = q.n; } }
function main(): i32 {
  const b = new Box();
  b.n = 40;
  const c = new C(b, [1, 2]);
  console.log(c.n + c.m);
  return 0;
}
