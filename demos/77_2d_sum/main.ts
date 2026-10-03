function main(): i32 {
  let t: i32 = 0;
  for (const row of [[1, 2], [3, 4]]) {
    t = t + row[0] + row[1];
  }
  console.log(t);
  return 0;
}
