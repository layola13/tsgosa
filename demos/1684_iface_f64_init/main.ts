interface Bx { rate: f64; n: i32; }
function main(): i32 {
  const b: Bx = { rate: 2.5, n: 4 };
  console.log(b.rate);
  console.log(b.n);
  return 0;
}
