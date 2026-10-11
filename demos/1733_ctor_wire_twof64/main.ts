class P { a: f64 = 0; b: f64 = 0; constructor() { this.a = 1.5; this.b = 2.5; } }
function main(): i32 {
  const p = new P();
  console.log(p.a + p.b);
  return 0;
}
