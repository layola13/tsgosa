let L = [1, 2, 3];
function main(): i32 {
  let s = 0;
  for (const x of L) {
    s = s + x;
  }
  console.log(s);
  console.log(L.map((x) => x * 2)[2]);
  return 0;
}
