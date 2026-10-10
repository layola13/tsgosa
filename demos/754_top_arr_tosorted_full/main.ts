const A = [3, 1, 2];
function main(): i32 {
  const s = A.toSorted();
  console.log(s[0] + s[1] + s[2] + s.length);
  return 0;
}
