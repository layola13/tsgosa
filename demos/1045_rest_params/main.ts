function sum(...xs: i32[]): i32 {
  let t = 0;
  for (const v of xs) { t += v; }
  return t;
}
function main(): i32 {
  console.log(sum(1, 2, 3));
  return 0;
}
