interface Box<T> { v: T; n: i32; }
function main(): i32 {
  const b: Box<f64> = { v: 3.5, n: 2 };
  console.log(b.v + b.n);
  return 0;
}
