function main(): i32 {
  const s: string = "a";
  switch (`${s}b`) {
    case "ab": console.log(1); break;
    default: console.log(0);
  }
  return 0;
}
