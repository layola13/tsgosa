interface Bx { rate: f64; n: i32; }
function sum(b: Bx): f64 { return b.rate + b.n; }
function main(): i32 {
  const b: Bx = { rate: 2.5, n: 4 };
  console.log(sum(b));
  return 0;
}
