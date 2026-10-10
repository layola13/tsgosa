interface O { a: i32; b: i32; }
function getObj(): O { return { a: 1, b: 2 }; }
function main(): i32 {
  let t = 0;
  for (const k in getObj()) { t += 1; }
  console.log(t);
  return 0;
}
