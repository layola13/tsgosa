class B { n: i32 = 0; constructor() { this.n = 1; } }
class D extends B { rate: f64 = 0; constructor() { super(); this.rate = 2.5; } }
function main(): i32 {
  const d = new D();
  console.log(d.n);
  console.log(d.rate);
  return 0;
}
