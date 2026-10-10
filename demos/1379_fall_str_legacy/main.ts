function main(): i32 {
  const s: string = "a";
  let n: i32 = 0;
  switch (s) {
    case "a":
      n = n + 1;
    case "b":
      n = n + 10;
      break;
    default:
      n = n + 100;
  }
  console.log(n);
  return 0;
}
