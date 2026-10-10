function main(): i32 {
  const n: i32 = 6;
  let s = "";
  for (let i = 0; i < n; i = i + 1) { s = s + (i < 3 ? "a" : "b"); }
  console.log(s);
  return 0;
}
