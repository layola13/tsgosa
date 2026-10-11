let n: i32 = 0;
function hit(): i32 {
  n = n + 1;
  return 7;
}
function main(): i32 {
  const a: i32[] = [];
  const [x = hit()] = a;
  console.log(x);
  console.log(n);
  return 0;
}
