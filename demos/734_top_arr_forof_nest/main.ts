const A = [1, 2], B = [3, 4];
function main(): i32 {
  let s = 0;
  for (const x of A) {
    for (const y of B) {
      s = s + x * y;
    }
  }
  console.log(s);
  return 0;
}
