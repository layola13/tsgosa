const E: i32[] = [];
function main(): i32 {
  let s = 0;
  for (const x of E) {
    s = s + x;
  }
  console.log(s);
  console.log(E.length);
  return 0;
}
