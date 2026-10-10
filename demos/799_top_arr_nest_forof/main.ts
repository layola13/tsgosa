const N = [[1, 2], [3]];
function main(): i32 {
  let s = 0;
  for (const row of N) {
    s = s + row[0] + row.length;
  }
  console.log(s);
  return 0;
}
