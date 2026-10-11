class B { n: i32 = 0; constructor() { this.n = 10; } }
class D extends B { m: i32 = 0; constructor() { super(); this.m = 3 * 3; } }
function main(): i32 {
  const d = new D();
  console.log(d.n);
  console.log(d.m);
  return 0;
}
