interface O { a: i32; }
function main(): i32 {
  const o: O = { a: 1 };
  let s = "";
  for (const k in o) { s = k; }
  console.log(s);
  return 0;
}
