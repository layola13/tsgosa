const A = [1, 2, 3, 4];
function main(): i32 {
  let s = 0;
  for (const x of A) {
    if (x == 3) {
      break;
    }
    s = s + x;
  }
  console.log(s);
  return 0;
}
