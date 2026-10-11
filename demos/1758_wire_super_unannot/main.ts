class B { rate: f64 = 0; constructor(p) { this.rate = p * 2; } }
class D extends B { constructor(q) { super(q); } }
function main(): i32 {
  const d = new D(1.5);
  console.log(d.rate);
  return 0;
}
