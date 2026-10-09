function f(x: string | null): void {
  const y = x ?? "d";
  if (y) {
    console.log(1);
  } else {
    console.log(0);
  }
}
f(null);
f("");
