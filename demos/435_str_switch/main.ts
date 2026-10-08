function main(): i32 {
  const s1 = "b";
  let n1 = 0;
  switch (s1) {
    case "a":
      n1 = 1;
      break;
    default:
      n1 = 9;
  }
  console.log(n1);
  const s2 = "c";
  let n2 = 0;
  switch (s2) {
    case "a":
      n2 = 1;
      break;
    case "b":
      n2 = 2;
      break;
    case "c":
      n2 = 3;
      break;
    case "d":
      n2 = 4;
      break;
    default:
      n2 = 9;
  }
  console.log(n2);
  return 0;
}
