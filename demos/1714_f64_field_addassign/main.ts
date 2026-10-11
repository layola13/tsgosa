class F { rate: f64 = 2.5; n: i32 = 1; }
function main(): i32 {
  const f = new F();
  f.rate += 1;
  console.log(f.rate);
  console.log(f.n);
  return 0;
}
