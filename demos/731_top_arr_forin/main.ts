const A = [7, 8, 9];
function main(): i32 {
  let s = 0;
  for (const i in A) {
    s = s + i;
  }
  console.log(s);
  return 0;
}
