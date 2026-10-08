function main(): i32 {
  const x = 5;
  let n = 0;
  switch (true) {
    case x > 3:
      n = 1;
      break;
    default:
      n = 2;
  }
  console.log(n);
  const y = 1;
  let m = 0;
  switch (true) {
    case y > 3:
      m = 1;
      break;
    default:
      m = 2;
  }
  console.log(m);
  return 0;
}
