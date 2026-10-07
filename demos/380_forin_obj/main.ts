interface O {
  a: i32;
  b: i32;
}
function main(): i32 {
  const o: O = { a: 1, b: 2 };
  let s: i32 = 0;
  for (const k in o) {
    console.log(k);
    s = s + 1;
  }
  console.log(s);
  return 0;
}
