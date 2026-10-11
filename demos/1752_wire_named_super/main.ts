class B { n: i32 = 0; constructor() { this.n = 10; } }
class D extends B { m: i32 = 0; constructor(k: i32) { super(); this.m = k * 2 + 1; } }
function main(): i32 {
  const d = new D(5);
  console.log(d.n);
  console.log(d.m);
  return 0;
}
