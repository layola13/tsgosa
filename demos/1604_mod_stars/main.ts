function main(): i32 {
  const n: i32 = 7;
  let f = "";
  for (let i = 0; i < n % 4; i = i + 1) { f = f + "*"; }
  console.log(f.length);
  return 0;
}
