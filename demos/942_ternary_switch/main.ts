function main(): i32 {
  const f = true;
  switch (f ? 1 : 0) {
    case 1: console.log(7); break;
    default: console.log(0);
  }
  return 0;
}
