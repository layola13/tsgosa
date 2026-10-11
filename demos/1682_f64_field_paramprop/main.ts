class P { constructor(public rate: f64, public n: i32) {} }
function main(): i32 {
  const p = new P(9.5, 3);
  console.log(p.rate);
  console.log(p.n);
  return 0;
}
