class W { rate: f64; n: i32 = 1; constructor(s: f64) { this.rate = s; } }
function main(): i32 {
  const w = new W(7.5);
  console.log(w.rate);
  console.log(w.n);
  return 0;
}
