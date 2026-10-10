function main(): i32 {
  const n: i32 = 4;
  let s = "";
  for (let i = 0; i < n; i = i + 1) { s = i % 2 == 0 ? s + "e" : s + "o"; }
  console.log(s);
  return 0;
}
