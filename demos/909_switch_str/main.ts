function main(): i32 {
  const s: string = "b";
  switch (s) {
    case "a": console.log(1); break;
    case "b": console.log(2); break;
    default: console.log(0);
  }
  return 0;
}
