function main(): i32 {
  const f = false;
  switch (f) {
    case true: console.log(1); break;
    case false: console.log(0); break;
    default: console.log(9);
  }
  return 0;
}
