function main(): i32 {
  const n: i32 = 2;
  let s: i32 = 0;
  switch (n) {
    case 1:
      s = s + 1;
      break;
    case 2:
      s = s + 10;
    default:
      s = s + 100;
  }
  console.log(s);
  return 0;
}
