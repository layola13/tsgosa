interface Bx { rate: f64; n: i32; }
function main(): i32 {
  const b: Bx = { rate: 2.5, n: 4 };
  const r: f64 = b.rate;
  console.log(r + 1);
  b.rate = 8.5;
  console.log(b.rate);
  console.log(b.n);
  return 0;
}
