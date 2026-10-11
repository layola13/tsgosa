class B { n: i32 = 0; constructor(v: i32) { this.n = v * 2; } }
class D extends B { m: i32 = 0; constructor(v: i32) { super(v); this.m = v + 1; } }
function main(): i32 {
  const d = new D(5);
  console.log(d.n + d.m);
  return 0;
}
