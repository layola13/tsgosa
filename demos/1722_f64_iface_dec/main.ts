interface Bx { rate: f64; n: i32; }
function main(): i32 {
  const b: Bx = { rate: 5.5, n: 2 };
  b.rate--;
  console.log(b.rate);
  b.rate -= 0.5;
  console.log(b.rate);
  console.log(b.n);
  return 0;
}
