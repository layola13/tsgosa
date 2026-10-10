function main(): i32 {
  const i: i32 = 3;
  let f = 1;
  for (let k = 1; k <= i; k = k + 1) { f = f * k; }
  console.log(f);
  return 0;
}
