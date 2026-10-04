function main(): i32 {
  let t: i32 = 0;
  for (let i: i32 = 1; i <= 5; i++) {
    t = t + i * i;
  }
  console.log(t);
  return 0;
}